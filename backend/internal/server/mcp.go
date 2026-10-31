package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm/clause"
)

const (
	mcpToolTimeout       = 15 * time.Second
	mcpMaxBodyBytes      = 1 << 20
	idempotencyRetention = 24 * time.Hour
)

const mcpInstructions = `Gradly is the user's grade tracker. Data is organised as semesters, which contain modules (courses).
Each module has up to four assessment grades - CC1, CC2, Lab (only when the module has a lab exam) and Exam - and a
grading formula that turns them into the module grade. Semester averages are coefficient-weighted.

Rules that matter:
- A missing grade is never a zero. Results report what is missing instead of guessing.
- Grades must be between 0 and the module's maximum grade (often 20).
- Formulas use the variables CC1, CC2, Lab, Exam; + - * / and parentheses; AVG, MIN, MAX, ABS, ROUND(x, digits).
- Call list_semesters / list_modules first to find ids; ids are never guessed.
- Create tools accept an optional idempotency_key: reuse the same key when retrying so nothing is created twice.
- Delete tools require confirm=true and cannot be undone.
Tools you don't see aren't permitted by this connection's permissions.`

// mcpServers caches one MCP server per distinct set of granted scopes, so tools/list only shows the tools a
// credential may call. There are at most 2^9 scope sets and schemas are shared through one SchemaCache.
type mcpServers struct {
	mu      sync.Mutex
	byScope map[string]*mcp.Server
	schemas *mcp.SchemaCache
}

func (a *App) mcpHandler() http.Handler {
	a.mcp = &mcpServers{byScope: map[string]*mcp.Server{}, schemas: mcp.NewSchemaCache()}
	return mcp.NewStreamableHTTPHandler(a.mcpServerFor, &mcp.StreamableHTTPOptions{
		// Stateless: every request is self-contained and authenticated on its own; nothing is kept between requests.
		Stateless:    true,
		JSONResponse: true,
		// DNS-rebinding protection is for unauthenticated local servers. Every request here needs a bearer token a
		// browser cannot attach on its own, and Gradly usually sits behind a local reverse proxy whose Host header
		// is the public name, which that check would reject.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        mcpMaxBodyBytes,
	})
}

func (a *App) mcpServerFor(r *http.Request) *mcp.Server {
	p := principalFrom(auth.TokenInfoFromContext(r.Context()))
	if p == nil {
		return nil
	}
	key := joinScopes(p.Scopes)
	a.mcp.mu.Lock()
	defer a.mcp.mu.Unlock()
	if s, ok := a.mcp.byScope[key]; ok {
		return s
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "gradly", Title: "Gradly", Version: "1.0.0"},
		&mcp.ServerOptions{Instructions: mcpInstructions, SchemaCache: a.mcp.schemas})
	a.registerTools(s, p.Scopes)
	a.mcp.byScope[key] = s
	return s
}

// ---------- tool plumbing ----------

type toolKind int

const (
	toolRead toolKind = iota
	toolCreate
	toolUpdate
	toolDelete
)

type toolSpec struct {
	Name        string
	Title       string
	Description string
	Scope       string
	Kind        toolKind
	Enums       map[string][]any // property -> allowed values, added to the inferred schema
}

func (k toolKind) category() string {
	switch k {
	case toolRead:
		return "read"
	case toolDelete:
		return "delete"
	default:
		return "write"
	}
}

func boolPtr(b bool) *bool { return &b }

func (s toolSpec) annotations() *mcp.ToolAnnotations {
	ann := &mcp.ToolAnnotations{Title: s.Title, OpenWorldHint: boolPtr(false)}
	switch s.Kind {
	case toolRead:
		ann.ReadOnlyHint = true
	case toolCreate:
		ann.DestructiveHint = boolPtr(false)
	case toolUpdate:
		ann.DestructiveHint, ann.IdempotentHint = boolPtr(true), true
	case toolDelete:
		ann.DestructiveHint, ann.IdempotentHint = boolPtr(true), true
	}
	return ann
}

// toolError is a tool failure with a stable category for the activity log.
type toolError struct {
	code string
	msg  string
}

func (e *toolError) Error() string { return e.msg }

func errPermissionFor(scope string) error {
	return &toolError{"permission_denied", fmt.Sprintf("This connection doesn't have the %q permission. "+
		"Ask the user to grant it in Gradly (Account settings → Developer integrations).", scope)}
}

// addTool registers a typed tool behind the checks every call must pass: an authenticated principal, the tool's
// scope, a timeout, and an activity-log entry. Errors from the service layer keep their user-facing message;
// anything unexpected is logged and replaced by a generic message, so internals never reach the client.
func addTool[In, Out any](a *App, s *mcp.Server, granted []string, spec toolSpec, run func(ctx context.Context, p *principal, in In) (Out, error)) {
	if !containsScope(granted, spec.Scope) {
		return
	}
	tool := &mcp.Tool{Name: spec.Name, Title: spec.Title, Description: spec.Description, Annotations: spec.annotations()}
	if len(spec.Enums) > 0 {
		schema, err := jsonschema.For[In](nil)
		if err != nil {
			panic(fmt.Sprintf("tool %s: %v", spec.Name, err))
		}
		for prop, values := range spec.Enums {
			if ps, ok := schema.Properties[prop]; ok {
				ps.Enum = values
			}
		}
		tool.InputSchema = schema
	}
	mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		start := time.Now()
		p := principalFrom(req.Extra.TokenInfo)
		if p == nil {
			return nil, zero, errors.New("authentication required")
		}
		if !p.has(spec.Scope) { // checked again per call: never rely on the tool list alone
			err := errPermissionFor(spec.Scope)
			a.logEvent(p, spec.Name, spec.Kind.category(), false, http.StatusForbidden, "permission_denied", time.Since(start))
			return nil, zero, err
		}
		ctx, cancel := context.WithTimeout(ctx, mcpToolTimeout)
		defer cancel()
		out, err := run(ctx, p, in)
		if err != nil {
			status, code, msg := classifyToolError(err)
			if status == http.StatusInternalServerError {
				slog.Error("mcp tool failed", "tool", spec.Name, "error", err)
			}
			a.logEvent(p, spec.Name, spec.Kind.category(), false, status, code, time.Since(start))
			return nil, zero, errors.New(msg)
		}
		a.logEvent(p, spec.Name, spec.Kind.category(), true, http.StatusOK, "", time.Since(start))
		return nil, out, nil
	})
}

func containsScope(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func classifyToolError(err error) (status int, code, msg string) {
	var te *toolError
	if errors.As(err, &te) {
		status := http.StatusBadRequest
		if te.code == "permission_denied" {
			status = http.StatusForbidden
		}
		return status, te.code, te.msg
	}
	var ue *userError
	if errors.As(err, &ue) {
		code := "validation"
		if ue.status == http.StatusNotFound {
			code = "not_found"
		}
		return ue.status, code, ue.msg
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "timeout", "The operation took too long. Try again."
	}
	return http.StatusInternalServerError, "internal", "An unexpected error occurred. Please try again."
}

// ---------- idempotency ----------

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)

// idempotent runs a create operation at most once per (credential, key). A retry with the same key returns the
// stored result of the first call; a concurrent duplicate is told to wait. Without a key, run is called directly.
func idempotent[T any](a *App, p *principal, op, key string, run func() (T, error)) (T, error) {
	var zero T
	if key == "" {
		return run()
	}
	if !idempotencyKeyPattern.MatchString(key) {
		return zero, &toolError{"validation", "idempotency_key must be 1-100 characters: letters, digits, '.', '_', ':' or '-'."}
	}
	now := a.now()
	a.DB.Where("created_at < ?", now.Add(-idempotencyRetention)).Delete(&models.IdempotencyRecord{})
	rec := models.IdempotencyRecord{CredentialID: p.CredentialID, Key: key, Operation: op, Pending: true, CreatedAt: now}
	// The primary key makes the claim atomic: only one call can insert the pending record.
	res := a.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec)
	if res.Error != nil {
		return zero, res.Error
	}
	if res.RowsAffected == 0 {
		var prev models.IdempotencyRecord
		if err := a.DB.Where("credential_id = ? AND key = ?", p.CredentialID, key).First(&prev).Error; err != nil {
			return zero, err
		}
		switch {
		case prev.Operation != op:
			return zero, &toolError{"validation", fmt.Sprintf("This idempotency_key was already used for %s. Use a new key for a different operation.", prev.Operation)}
		case prev.Pending:
			return zero, &toolError{"conflict", "A request with this idempotency_key is still being processed. Wait a moment and try again."}
		}
		var out T
		if err := json.Unmarshal([]byte(prev.Result), &out); err != nil {
			return zero, err
		}
		return out, nil
	}
	out, err := run()
	if err != nil {
		a.DB.Where("credential_id = ? AND key = ?", p.CredentialID, key).Delete(&models.IdempotencyRecord{})
		return zero, err
	}
	b, _ := json.Marshal(out)
	a.DB.Model(&models.IdempotencyRecord{}).Where("credential_id = ? AND key = ?", p.CredentialID, key).
		Updates(map[string]any{"result": string(b), "pending": false})
	return out, nil
}

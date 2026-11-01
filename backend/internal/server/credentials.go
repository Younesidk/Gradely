package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/younesidk/gradely/internal/models"
	"gorm.io/gorm"
)

// Every integration secret is "<prefix><43 url-safe chars>" carrying 256 bits from crypto/rand. The prefixes are not
// secret: they say what a leaked string is, and let the endpoint route it without guessing.
const (
	apiKeyPrefix       = "gradly_sk_"
	accessTokenPrefix  = "gradly_at_"
	refreshTokenPrefix = "gradly_rt_"
	keyDisplayLength   = len(apiKeyPrefix) + 6

	credentialAPIKey = "api_key"
	credentialOAuth  = "oauth"

	// usageWriteInterval throttles last-used timestamps; the request counter is still exact.
	usageWriteInterval = time.Minute
)

func newSecret(prefix string) string { return prefix + newToken() }

// principal is the authenticated caller of the MCP endpoint: a user acting through one credential. Its user and
// scopes come only from the credential, never from anything the client sends.
type principal struct {
	User         *models.User
	Kind         string // credentialAPIKey or credentialOAuth
	CredentialID string // API key id, or OAuth grant id
	Name         string // API key name, or OAuth client name
	Scopes       []string
	lastUsed     *time.Time
}

func (p *principal) has(scope string) bool { return slices.Contains(p.Scopes, scope) }
func (p *principal) limiterKey() string    { return p.Kind + ":" + p.CredentialID }

const principalExtraKey = "gradly.principal"

func principalFrom(ti *auth.TokenInfo) *principal {
	if ti == nil {
		return nil
	}
	p, _ := ti.Extra[principalExtraKey].(*principal)
	return p
}

// activeUser loads a user that may still use the API: disabling an account cuts off its integrations immediately.
func (a *App) activeUser(id string) (*models.User, error) {
	var u models.User
	if err := a.DB.First(&u, "id = ?", id).Error; err != nil || u.IsDisabled {
		return nil, auth.ErrInvalidToken
	}
	return &u, nil
}

// authenticate resolves a bearer token to a principal. Revocation, expiry, disabling and audience are all checked
// against the database on every request, so they take effect at once.
func (a *App) authenticate(token string) (*principal, error) {
	if len(token) > 200 {
		return nil, auth.ErrInvalidToken
	}
	now := a.now()
	switch {
	case strings.HasPrefix(token, apiKeyPrefix):
		var k models.APIKey
		if err := a.DB.Where("key_hash = ?", hashToken(token)).First(&k).Error; err != nil {
			return nil, auth.ErrInvalidToken
		}
		if k.RevokedAt != nil || (k.ExpiresAt != nil && !k.ExpiresAt.After(now)) {
			return nil, auth.ErrInvalidToken
		}
		u, err := a.activeUser(k.UserID)
		if err != nil {
			return nil, err
		}
		return &principal{User: u, Kind: credentialAPIKey, CredentialID: k.ID, Name: k.Name, Scopes: splitScopes(k.Scopes), lastUsed: k.LastUsedAt}, nil

	case strings.HasPrefix(token, accessTokenPrefix):
		var t models.OAuthToken
		if err := a.DB.Where("token_hash = ? AND kind = ?", hashToken(token), models.TokenAccess).First(&t).Error; err != nil {
			return nil, auth.ErrInvalidToken
		}
		if !t.ExpiresAt.After(now) || t.Resource != a.Cfg.MCPResource() {
			return nil, auth.ErrInvalidToken
		}
		var g models.OAuthGrant
		if err := a.DB.Where("id = ? AND revoked_at IS NULL", t.GrantID).First(&g).Error; err != nil || g.UserID != t.UserID {
			return nil, auth.ErrInvalidToken
		}
		u, err := a.activeUser(t.UserID)
		if err != nil {
			return nil, err
		}
		var client models.OAuthClient
		name := "OAuth application"
		if a.DB.Select("name").First(&client, "id = ?", g.ClientID).Error == nil {
			name = client.Name
		}
		return &principal{User: u, Kind: credentialOAuth, CredentialID: g.ID, Name: name, Scopes: splitScopes(t.Scopes), lastUsed: g.LastUsedAt}, nil
	}
	return nil, auth.ErrInvalidToken
}

// recordUsage counts an accepted request against its credential (shown in the dashboard).
func (a *App) recordUsage(p *principal) {
	now := a.now()
	updates := map[string]any{"request_count": gorm.Expr("request_count + 1")}
	if p.lastUsed == nil || now.Sub(*p.lastUsed) >= usageWriteInterval {
		updates["last_used_at"] = now
	}
	model := any(&models.APIKey{})
	if p.Kind == credentialOAuth {
		model = &models.OAuthGrant{}
	}
	a.DB.Model(model).Where("id = ?", p.CredentialID).Updates(updates)
}

// ---------- MCP endpoint middleware ----------

type clientIPKey struct{}

const maxAuthFailuresPerMinute = 20

// mcpEntry is the Gin handler for /mcp: CORS, a per-IP brake on failed authentication, then the bearer-token check
// (which also populates the MCP SDK's token info), the rate limit and finally the MCP transport.
func (a *App) mcpEntry(inner http.Handler) gin.HandlerFunc {
	protected := auth.RequireBearerToken(a.verifyBearer, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: a.Cfg.BaseURL + "/.well-known/oauth-protected-resource/mcp",
		// Expiry is checked in verifyBearer against the database; keys without an expiry date are valid.
		AllowMissingExpiration: true,
	})(a.mcpRateLimit(inner))

	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
		h.Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id, Retry-After, X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset")
		h.Set("Cache-Control", "no-store")
		if c.Request.Method == http.MethodOptions {
			c.Status(http.StatusNoContent)
			return
		}
		ip := c.ClientIP()
		if a.authFailures.count("ip:"+ip, a.now()) >= maxAuthFailuresPerMinute {
			h.Set("Retry-After", "60")
			writeRPCError(c.Writer, http.StatusTooManyRequests, "Too many failed authentication attempts. Wait a minute and try again.")
			return
		}
		ctx := context.WithValue(c.Request.Context(), clientIPKey{}, ip)
		protected.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
	}
}

// verifyBearer adapts authenticate to the MCP SDK's TokenVerifier.
func (a *App) verifyBearer(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	p, err := a.authenticate(token)
	if err != nil {
		if ip, _ := ctx.Value(clientIPKey{}).(string); ip != "" {
			a.authFailures.take(a.now(), limitCheck{"ip:" + ip, maxAuthFailuresPerMinute})
		}
		return nil, fmt.Errorf("%w: the token is invalid, expired or revoked", auth.ErrInvalidToken)
	}
	return &auth.TokenInfo{Scopes: p.Scopes, UserID: p.User.ID, Extra: map[string]any{principalExtraKey: p}}, nil
}

// mcpRateLimit enforces the per-credential and per-user sliding windows before the request reaches the MCP server,
// so a rejected request never runs a tool.
func (a *App) mcpRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(auth.TokenInfoFromContext(r.Context()))
		if p == nil {
			writeRPCError(w, http.StatusUnauthorized, "Authentication required.")
			return
		}
		now := a.now()
		res := a.mcpLimiter.take(now,
			limitCheck{p.limiterKey(), a.Cfg.CredentialRateLimit},
			limitCheck{"user:" + p.User.ID, a.Cfg.UserRateLimit})
		h := w.Header()
		h.Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
		h.Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
		h.Set("X-RateLimit-Reset", strconv.FormatInt(res.Reset.Unix(), 10))
		if !res.Allowed {
			retry := int(res.RetryAfter.Seconds() + 0.999)
			if retry < 1 {
				retry = 1
			}
			h.Set("Retry-After", strconv.Itoa(retry))
			if a.mcpLimiter.shouldLogRejection(p.limiterKey(), now) {
				a.logEvent(p, "rate_limited", "limit", false, http.StatusTooManyRequests, "rate_limited", 0)
			}
			writeRPCError(w, http.StatusTooManyRequests, fmt.Sprintf(
				"Rate limit exceeded: at most %d requests per %s. Try again in %d seconds.", res.Limit, a.windowText(), retry))
			return
		}
		a.recordUsage(p)
		next.ServeHTTP(w, r)
	})
}

func (a *App) windowText() string {
	w := a.Cfg.RateLimitWindow
	if w%time.Minute == 0 {
		n := int(w / time.Minute)
		if n == 1 {
			return "minute"
		}
		return strconv.Itoa(n) + " minutes"
	}
	return w.String()
}

// writeRPCError answers with a JSON-RPC error body, which MCP clients can surface, alongside the HTTP status.
func writeRPCError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32000, "message": msg},
	})
}

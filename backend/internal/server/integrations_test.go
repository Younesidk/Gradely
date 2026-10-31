package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/younesidk/gradely/internal/config"
	"github.com/younesidk/gradely/internal/models"
)

// ---------- harness ----------

var fullAccess = allScopes()

func (c *client) createKey(t *testing.T, name string, scopes []string) (id, secret string) {
	t.Helper()
	r := expect(t, c.post("/api/integrations/keys", map[string]any{"name": name, "scopes": scopes}), 201)
	m := r.json()
	return m["key"].(map[string]any)["id"].(string), m["secret"].(string)
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// mcpSession connects the official MCP Go client to /mcp over Streamable HTTP with the given bearer token.
func (e *env) mcpSession(token string) (*mcp.ClientSession, error) {
	cl := mcp.NewClient(&mcp.Implementation{Name: "gradly-test", Version: "1"}, nil)
	tr := &mcp.StreamableClientTransport{Endpoint: e.srv.URL + "/mcp", MaxRetries: -1, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: bearer{token, http.DefaultTransport}}}
	return cl.Connect(context.Background(), tr, nil)
}

func (e *env) mustSession(token string) *mcp.ClientSession {
	e.t.Helper()
	s, err := e.mcpSession(token)
	if err != nil {
		e.t.Fatalf("connect: %v", err)
	}
	e.t.Cleanup(func() { s.Close() })
	return s
}

type toolResult struct {
	isError bool
	text    string
	data    map[string]any
}

func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) toolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return toolResult{isError: true, text: err.Error()}
	}
	out := toolResult{isError: res.IsError}
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			out.text = tc.Text
		}
	}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out.data)
	}
	return out
}

func ok(t *testing.T, r toolResult) map[string]any {
	t.Helper()
	if r.isError {
		t.Fatalf("tool failed: %s", r.text)
	}
	return r.data
}

func failsWith(t *testing.T, r toolResult, want string) {
	t.Helper()
	if !r.isError || !strings.Contains(r.text, want) {
		t.Fatalf("want error containing %q, got isError=%v %q", want, r.isError, r.text)
	}
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// rawMCP sends one JSON-RPC request to /mcp and returns the HTTP response (rate-limit tests need the headers).
func (e *env) rawMCP(token, method string, params any) resp {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	buf := new(strings.Builder)
	b := make([]byte, 4096)
	for {
		n, err := res.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	return resp{res.StatusCode, []byte(buf.String()), res.Header}
}

func (e *env) ping(token string) resp { return e.rawMCP(token, "ping", map[string]any{}) }

func (e *env) countSemesters(userName string) int64 {
	var u models.User
	e.app.DB.Where("user_name = ?", userName).First(&u)
	var n int64
	e.app.DB.Model(&models.Semester{}).Where("user_id = ?", u.ID).Count(&n)
	return n
}

// ---------- API keys ----------

func TestAPIKeyLifecycle(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")

	r := expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "Hermes", "description": "agent", "scopes": fullAccess}), 201)
	secret := r.json()["secret"].(string)
	key := r.json()["key"].(map[string]any)
	if !strings.HasPrefix(secret, apiKeyPrefix) || len(secret) != len(apiKeyPrefix)+43 {
		t.Fatalf("unexpected key format %q", secret)
	}
	if r.header.Get("Cache-Control") != "no-store" {
		t.Error("the response carrying the secret must not be cached")
	}
	if !strings.HasPrefix(secret, strings.TrimSuffix(key["prefix"].(string), "…")) {
		t.Error("the displayed prefix must be the start of the key")
	}

	// Stored as a hash only; the secret never comes back.
	var row models.APIKey
	e.app.DB.First(&row, "id = ?", key["id"])
	sum := sha256.Sum256([]byte(secret))
	if row.KeyHash != fmt.Sprintf("%x", sum) {
		t.Error("the key must be stored as its SHA-256")
	}
	raw, _ := json.Marshal(row)
	if strings.Contains(string(raw), secret[len(apiKeyPrefix)+6:]) {
		t.Fatal("the secret must not be stored")
	}
	list := expect(t, alice.get("/api/integrations/keys"), 200)
	if strings.Contains(string(list.body), secret) {
		t.Fatal("listing keys must never return the secret")
	}

	// It authenticates.
	s := e.mustSession(secret)
	ok(t, call(t, s, "list_semesters", nil))

	// Rename and narrow permissions; the narrower scopes apply at once.
	id := key["id"].(string)
	expect(t, alice.put("/api/integrations/keys/"+id, map[string]any{"name": "Hermes 2", "scopes": []string{ScopeGradesRead}}), 200)
	s2 := e.mustSession(secret)
	if names := toolNames(t, s2); slices.Contains(names, "list_semesters") || !slices.Contains(names, "list_grades") {
		t.Fatalf("tools after narrowing: %v", names)
	}
	failsWith(t, call(t, s2, "list_semesters", nil), "") // not offered any more

	// Rotate: the old secret dies, the new one works, the record keeps its identity.
	rot := expect(t, alice.post("/api/integrations/keys/"+id+"/rotate", nil), 200)
	newSecret := rot.json()["secret"].(string)
	if newSecret == secret || rot.json()["key"].(map[string]any)["id"] != id {
		t.Fatal("rotation must issue a new secret on the same key")
	}
	expect(t, e.ping(secret), 401)
	expect(t, e.ping(newSecret), 200)

	// Revoke: immediate. Delete only once revoked.
	expect(t, alice.del("/api/integrations/keys/"+id), 400)
	expect(t, alice.post("/api/integrations/keys/"+id+"/revoke", nil), 200)
	expect(t, e.ping(newSecret), 401)
	expect(t, alice.post("/api/integrations/keys/"+id+"/rotate", nil), 400)
	expect(t, alice.put("/api/integrations/keys/"+id, map[string]any{"name": "x", "scopes": fullAccess}), 400)
	expect(t, alice.del("/api/integrations/keys/"+id), 200)
	expect(t, alice.del("/api/integrations/keys/"+id), 404)
}

func TestAPIKeyValidationAndExpiry(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "", "scopes": fullAccess}), 400)
	expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "x", "scopes": []string{}}), 400)
	r := expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "x", "scopes": []string{"grades:delete"}}), 400)
	if !strings.Contains(r.errText(), "grades:delete") {
		t.Errorf("unknown scope message: %q", r.errText())
	}
	expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "x", "scopes": fullAccess, "expiresAt": "2020-01-01"}), 400)
	expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "x", "scopes": fullAccess, "expiresAt": "soon"}), 400)

	r = expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "short", "scopes": fullAccess, "expiresAt": "2026-11-02"}), 201)
	secret := r.json()["secret"].(string)
	expect(t, e.ping(secret), 200)
	e.now = time.Date(2026, 11, 2, 23, 59, 0, 0, time.UTC) // still within the last day
	expect(t, e.ping(secret), 200)
	e.now = time.Date(2026, 11, 3, 0, 0, 1, 0, time.UTC)
	expect(t, e.ping(secret), 401)
	expect(t, alice.login("alice", "Student-passw0rd"), 200) // the web session itself expired meanwhile
	keys := expect(t, alice.get("/api/integrations/keys"), 200).json()["items"].([]any)
	if keys[0].(map[string]any)["status"] != "expired" {
		t.Error("an expired key should be reported as expired")
	}
	expect(t, alice.post("/api/integrations/keys/"+keys[0].(map[string]any)["id"].(string)+"/rotate", nil), 400)
}

func TestAPIKeyLimitPerUser(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	for i := 0; i < maxActiveAPIKeys; i++ {
		alice.createKey(t, fmt.Sprint("k", i), []string{ScopeGradesRead})
	}
	expect(t, alice.post("/api/integrations/keys", map[string]any{"name": "one too many", "scopes": fullAccess}), 400)
}

func TestInvalidTokensAreRejected(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "k", fullAccess)

	for _, tok := range []string{"", "nonsense", apiKeyPrefix + "AAAA", secret + "x", strings.Replace(secret, apiKeyPrefix, accessTokenPrefix, 1),
		strings.Replace(secret, apiKeyPrefix, refreshTokenPrefix, 1), strings.Repeat("a", 500)} {
		r := e.ping(tok)
		if r.status != 401 {
			t.Fatalf("token %q: status %d", tok, r.status)
		}
		if www := r.header.Get("WWW-Authenticate"); !strings.Contains(www, "resource_metadata=") || !strings.Contains(www, "/.well-known/oauth-protected-resource/mcp") {
			t.Fatalf("401 must point at the protected resource metadata, got %q", www)
		}
	}
	// A session cookie is not an MCP credential.
	expect(t, alice.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}), 401)
}

func TestFailedAuthenticationIsThrottledPerAddress(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < maxAuthFailuresPerMinute; i++ {
		expect(t, e.ping("gradly_sk_wrong"), 401)
	}
	r := expect(t, e.ping("gradly_sk_wrong"), 429)
	if r.header.Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
	e.now = e.now.Add(61 * time.Second)
	expect(t, e.ping("gradly_sk_wrong"), 401)
}

func TestAPIKeysAreIsolatedBetweenUsers(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	alice, bob := e.student(admin, "alice"), e.student(admin, "bob")
	id, secret := alice.createKey(t, "mine", fullAccess)

	if strings.Contains(string(expect(t, bob.get("/api/integrations/keys"), 200).body), id) {
		t.Fatal("bob can see alice's key")
	}
	expect(t, bob.put("/api/integrations/keys/"+id, map[string]any{"name": "pwned", "scopes": fullAccess}), 404)
	expect(t, bob.post("/api/integrations/keys/"+id+"/rotate", nil), 404)
	expect(t, bob.post("/api/integrations/keys/"+id+"/revoke", nil), 404)
	expect(t, bob.del("/api/integrations/keys/"+id), 404)
	expect(t, e.ping(secret), 200)
	expect(t, e.client().get("/api/integrations/keys"), 401)
}

func TestDisablingAnAccountStopsItsIntegrations(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	alice := e.student(admin, "alice")
	_, secret := alice.createKey(t, "k", fullAccess)
	expect(t, e.ping(secret), 200)
	var u models.User
	e.app.DB.Where("user_name = ?", "alice").First(&u)
	expect(t, admin.post("/api/admin/users/"+u.ID+"/disabled", map[string]any{"disabled": true}), 200)
	expect(t, e.ping(secret), 401)
}

// ---------- MCP tools ----------

func TestMCPToolsEndToEnd(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "Hermes", fullAccess)
	s := e.mustSession(secret)

	names := toolNames(t, s)
	for _, want := range []string{"list_semesters", "create_semester", "update_semester", "delete_semester", "list_modules", "create_module",
		"add_grade", "update_grade", "delete_grade", "get_module_average", "get_semester_average", "get_formula", "update_formula",
		"get_missing_assessments", "get_semester_progress", "get_grades_by_module", "get_overall_statistics", "get_grade_statistics"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing tool %s", want)
		}
	}

	sem := ok(t, call(t, s, "create_semester", map[string]any{"name": "S1", "academic_year": "2026-2027"}))["semester"].(map[string]any)
	semID := sem["id"].(string)
	mod := ok(t, call(t, s, "create_module", map[string]any{"semester_id": semID, "name": "Algebra", "coefficient": 3}))["module"].(map[string]any)
	modID := mod["id"].(string)
	if mod["max_grade"].(float64) != 20 || mod["formula_name"] != defaultNoLabName {
		t.Fatalf("module defaults: %v", mod)
	}

	// Grades: default formula (CC1 + CC2) / 2 * 0.4 + Exam * 0.6
	ok(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "CC1", "value": 12}))
	failsWith(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "cc2", "value": 14}), "CC2") // exact names only
	ch := ok(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "CC2", "value": 14}))
	if req := ch["result"].(map[string]any)["required_exam"].(map[string]any); req["kind"] != "needed" {
		t.Fatalf("required exam: %v", req)
	}
	failsWith(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "CC1", "value": 15}), "already has a grade")
	failsWith(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "Exam", "value": 21}), "between 0 and 20")
	failsWith(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "Lab", "value": 10}), "no lab exam")
	failsWith(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "CC3", "value": 10}), "")
	ch = ok(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "Exam", "value": 10}))
	res := ch["result"].(map[string]any)
	if res["grade"].(float64) != 11.2 || res["passed"] != true { // (12+14)/2*0.4 + 10*0.6
		t.Fatalf("result: %v", res)
	}
	ch = ok(t, call(t, s, "update_grade", map[string]any{"module_id": modID, "assessment": "CC1", "value": 16}))
	if ch["previous_value"].(float64) != 12 || ch["result"].(map[string]any)["grade"].(float64) != 12 {
		t.Fatalf("update: %v", ch)
	}

	// The MCP result matches what the web app computes for the same module.
	view := expect(t, alice.get("/api/semesters/"+semID), 200).json()["view"].(map[string]any)
	webGrade := view["modules"].([]any)[0].(map[string]any)["result"].(map[string]any)["grade"].(float64)
	avg := ok(t, call(t, s, "get_module_average", map[string]any{"module_id": modID}))
	if avg["grade"].(float64) != webGrade {
		t.Fatalf("MCP grade %v != web grade %v", avg["grade"], webGrade)
	}
	semAvg := ok(t, call(t, s, "get_semester_average", map[string]any{"semester_id": semID}))
	if semAvg["summary"].(map[string]any)["average"].(float64) != view["summary"].(map[string]any)["average"].(float64) {
		t.Fatal("semester average differs from the web app")
	}

	// Removing a grade makes it missing, not zero.
	ch = ok(t, call(t, s, "delete_grade", map[string]any{"module_id": modID, "assessment": "Exam"}))
	if ch["value"] != nil || ch["result"].(map[string]any)["grade"] != nil {
		t.Fatalf("after delete: %v", ch)
	}
	miss := ok(t, call(t, s, "get_missing_assessments", nil))
	if miss["total_missing"].(float64) != 1 {
		t.Fatalf("missing: %v", miss)
	}
	prog := ok(t, call(t, s, "get_semester_progress", map[string]any{"semester_id": semID}))
	if prog["assessments_expected"].(float64) != 3 || prog["assessments_entered"].(float64) != 2 {
		t.Fatalf("progress: %v", prog)
	}

	// Formulas: an update recalculates dependent modules through the same engine.
	ok(t, call(t, s, "update_grade", map[string]any{"module_id": modID, "assessment": "Exam", "value": 10}))
	formulas := ok(t, call(t, s, "list_formulas", nil))["formulas"].([]any)
	fid := formulas[0].(map[string]any)["id"].(string)
	failsWith(t, call(t, s, "update_formula", map[string]any{"formula_id": fid, "expression": "CC1 +"}), "")
	failsWith(t, call(t, s, "update_formula", map[string]any{"formula_id": fid, "expression": "Lab"}), "")
	ok(t, call(t, s, "update_formula", map[string]any{"formula_id": fid, "expression": "Exam"}))
	if g := ok(t, call(t, s, "get_module_average", map[string]any{"module_id": modID}))["grade"].(float64); g != 10 {
		t.Fatalf("grade after formula change = %v, want 10", g)
	}
	v := ok(t, call(t, s, "validate_formula", map[string]any{"expression": "AVG(CC1, CC2) * 0.5 + Exam * 0.5"}))
	if v["valid"] != true || len(v["variables"].([]any)) != 3 {
		t.Fatalf("validate: %v", v)
	}
	if ok(t, call(t, s, "validate_formula", map[string]any{"expression": "CC1 * "}))["valid"] != false {
		t.Fatal("invalid formula reported valid")
	}
	failsWith(t, call(t, s, "delete_formula", map[string]any{"formula_id": fid, "confirm": true}), "used by 1 module")

	// Semester update is partial; deletes need confirmation.
	upd := ok(t, call(t, s, "update_semester", map[string]any{"semester_id": semID, "start_date": "2026-09-01"}))["semester"].(map[string]any)
	if upd["name"] != "S1" || upd["academic_year"] != "2026-2027" || upd["start_date"] != "2026-09-01" {
		t.Fatalf("partial update: %v", upd)
	}
	failsWith(t, call(t, s, "update_semester", map[string]any{"semester_id": semID, "end_date": "2026-01-01"}), "before the start date")
	failsWith(t, call(t, s, "delete_module", map[string]any{"module_id": modID}), "confirm=true")
	failsWith(t, call(t, s, "delete_semester", map[string]any{"semester_id": semID, "confirm": false}), "confirm=true")
	ok(t, call(t, s, "delete_semester", map[string]any{"semester_id": semID, "confirm": true}))
	var left int64
	e.app.DB.Model(&models.Module{}).Where("id = ?", modID).Count(&left)
	if left != 0 {
		t.Fatal("deleting a semester must delete its modules")
	}
}

func TestMCPLabModulesAndPartialModuleUpdate(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "k", fullAccess)
	s := e.mustSession(secret)
	semID := ok(t, call(t, s, "create_semester", map[string]any{"name": "S"}))["semester"].(map[string]any)["id"].(string)
	lab := ok(t, call(t, s, "create_formula", map[string]any{"name": "Lab mix", "expression": "Lab * 0.5 + Exam * 0.5", "for_lab_exam": true}))["formula"].(map[string]any)
	mod := ok(t, call(t, s, "create_module", map[string]any{"semester_id": semID, "name": "Physics", "has_lab_exam": true}))["module"].(map[string]any)
	if mod["formula_id"] != lab["id"] {
		t.Fatalf("a lab module should get the lab default formula, got %v", mod["formula_id"])
	}
	modID := mod["id"].(string)
	ok(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "Lab", "value": 18}))
	ok(t, call(t, s, "add_grade", map[string]any{"module_id": modID, "assessment": "Exam", "value": 8}))
	if g := ok(t, call(t, s, "get_module_average", map[string]any{"module_id": modID}))["grade"].(float64); g != 13 {
		t.Fatalf("lab grade = %v", g)
	}
	// Max grade can't drop below an existing grade.
	failsWith(t, call(t, s, "update_module", map[string]any{"module_id": modID, "max_grade": 10}), "between 0 and 10")
	upd := ok(t, call(t, s, "update_module", map[string]any{"module_id": modID, "coefficient": 4}))["module"].(map[string]any)
	if upd["coefficient"].(float64) != 4 || upd["name"] != "Physics" || upd["formula_id"] != lab["id"] {
		t.Fatalf("partial module update: %v", upd)
	}
	// Turning the lab off switches to the no-lab default.
	upd = ok(t, call(t, s, "update_module", map[string]any{"module_id": modID, "has_lab_exam": false}))["module"].(map[string]any)
	if upd["formula_name"] != defaultNoLabName {
		t.Fatalf("formula after removing the lab: %v", upd["formula_name"])
	}
	failsWith(t, call(t, s, "set_module_formula", map[string]any{"module_id": modID, "formula_id": lab["id"]}), "without a lab exam")
}

func TestMCPIdempotentCreates(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "k", fullAccess)
	s := e.mustSession(secret)
	a := ok(t, call(t, s, "create_semester", map[string]any{"name": "Retry me", "idempotency_key": "req-1"}))
	b := ok(t, call(t, s, "create_semester", map[string]any{"name": "Retry me", "idempotency_key": "req-1"}))
	if !reflect.DeepEqual(a, b) || e.countSemesters("alice") != 1 {
		t.Fatalf("a retried create must not duplicate: %v vs %v, count %d", a, b, e.countSemesters("alice"))
	}
	failsWith(t, call(t, s, "create_formula", map[string]any{"name": "F", "expression": "Exam", "idempotency_key": "req-1"}), "already used for create_semester")
	failsWith(t, call(t, s, "create_semester", map[string]any{"name": "x", "idempotency_key": "bad key!"}), "idempotency_key")
	// A failed create frees the key.
	failsWith(t, call(t, s, "create_semester", map[string]any{"name": "", "idempotency_key": "req-2"}), "name")
	ok(t, call(t, s, "create_semester", map[string]any{"name": "Second", "idempotency_key": "req-2"}))
	// Keys expire after a day.
	e.now = e.now.Add(25 * time.Hour)
	ok(t, call(t, s, "create_semester", map[string]any{"name": "Retry me", "idempotency_key": "req-1"}))
	if n := e.countSemesters("alice"); n != 3 {
		t.Fatalf("semesters = %d, want 3", n)
	}
}

func TestMCPPermissionsAreEnforced(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, full := alice.createKey(t, "full", fullAccess)
	fs := e.mustSession(full)
	semID := ok(t, call(t, fs, "create_semester", map[string]any{"name": "S"}))["semester"].(map[string]any)["id"].(string)
	modID := ok(t, call(t, fs, "create_module", map[string]any{"semester_id": semID, "name": "M"}))["module"].(map[string]any)["id"].(string)

	_, ro := alice.createKey(t, "read only", readScopes())
	rs := e.mustSession(ro)
	for _, n := range toolNames(t, rs) {
		if strings.HasPrefix(n, "create_") || strings.HasPrefix(n, "update_") || strings.HasPrefix(n, "delete_") || n == "add_grade" || strings.HasPrefix(n, "set_") {
			t.Errorf("read-only key is offered %s", n)
		}
	}
	for _, try := range []struct {
		tool string
		args map[string]any
	}{
		{"add_grade", map[string]any{"module_id": modID, "assessment": "CC1", "value": 10}},
		{"update_grade", map[string]any{"module_id": modID, "assessment": "CC1", "value": 10}},
		{"create_semester", map[string]any{"name": "X"}},
		{"delete_semester", map[string]any{"semester_id": semID, "confirm": true}},
		{"update_formula", map[string]any{"formula_id": "x", "expression": "Exam"}},
	} {
		if r := call(t, rs, try.tool, try.args); !r.isError {
			t.Errorf("read-only key could call %s", try.tool)
		}
	}
	var m models.Module
	e.app.DB.First(&m, "id = ?", modID)
	if m.CC1 != nil || e.countSemesters("alice") != 1 {
		t.Fatal("a read-only key changed data")
	}

	// grades:write without semesters:write can grade but not create or delete semesters.
	_, gw := alice.createKey(t, "grader", []string{ScopeGradesWrite})
	gs := e.mustSession(gw)
	ok(t, call(t, gs, "add_grade", map[string]any{"module_id": modID, "assessment": "CC1", "value": 10}))
	failsWith(t, call(t, gs, "create_semester", map[string]any{"name": "X"}), "")
	failsWith(t, call(t, gs, "list_grades", map[string]any{"module_id": modID}), "") // write doesn't imply read

	// Defense in depth: even if a cached server offered a tool, the per-call check refuses it.
	p := &principal{User: &models.User{ID: "u"}, Kind: credentialAPIKey, CredentialID: "c", Name: "n", Scopes: []string{ScopeGradesRead}}
	if p.has(ScopeGradesWrite) {
		t.Fatal("scope check")
	}
}

func TestMCPCannotReachAnotherUsersData(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	alice, bob := e.student(admin, "alice"), e.student(admin, "bob")
	bobSem := bob.newSemester(t, "Bob's")
	bobMod := id(expect(t, bob.post("/api/semesters/"+bobSem+"/modules", moduleBody("Secret")), 201))
	bobFormulas := expect(t, bob.get("/api/formulas"), 200).json()["formulas"].([]any)
	bobFormula := bobFormulas[0].(map[string]any)["id"].(string)

	_, secret := alice.createKey(t, "k", fullAccess)
	s := e.mustSession(secret)
	for _, try := range []struct {
		tool string
		args map[string]any
	}{
		{"get_semester", map[string]any{"semester_id": bobSem}},
		{"list_modules", map[string]any{"semester_id": bobSem}},
		{"get_module", map[string]any{"module_id": bobMod}},
		{"list_grades", map[string]any{"module_id": bobMod}},
		{"add_grade", map[string]any{"module_id": bobMod, "assessment": "CC1", "value": 1}},
		{"update_module", map[string]any{"module_id": bobMod, "name": "pwned"}},
		{"delete_module", map[string]any{"module_id": bobMod, "confirm": true}},
		{"create_module", map[string]any{"semester_id": bobSem, "name": "intruder"}},
		{"update_semester", map[string]any{"semester_id": bobSem, "name": "pwned"}},
		{"delete_semester", map[string]any{"semester_id": bobSem, "confirm": true}},
		{"get_formula", map[string]any{"formula_id": bobFormula}},
		{"update_formula", map[string]any{"formula_id": bobFormula, "expression": "Exam"}},
		{"delete_formula", map[string]any{"formula_id": bobFormula, "confirm": true}},
		{"get_semester_average", map[string]any{"semester_id": bobSem}},
	} {
		failsWith(t, call(t, s, try.tool, try.args), "not found")
	}
	// Alice's own module can't use Bob's formula either.
	sem := ok(t, call(t, s, "create_semester", map[string]any{"name": "A"}))["semester"].(map[string]any)["id"].(string)
	failsWith(t, call(t, s, "create_module", map[string]any{"semester_id": sem, "name": "M", "formula_id": bobFormula}), "isn't available")

	view := expect(t, bob.get("/api/semesters/"+bobSem), 200).json()["view"].(map[string]any)
	m := view["modules"].([]any)[0].(map[string]any)["module"].(map[string]any)
	if view["semester"].(map[string]any)["name"] != "Bob's" || m["name"] != "Secret" || m["cc1"] != nil || len(view["modules"].([]any)) != 1 {
		t.Fatalf("bob's data changed: %v", view)
	}
}

func TestMCPRejectsInvalidArguments(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "k", fullAccess)
	s := e.mustSession(secret)
	failsWith(t, call(t, s, "create_semester", map[string]any{}), "")                                       // missing required
	failsWith(t, call(t, s, "create_semester", map[string]any{"name": "x", "user_id": "someone-else"}), "") // unknown property
	failsWith(t, call(t, s, "create_semester", map[string]any{"name": 42}), "")                             // wrong type
	failsWith(t, call(t, s, "get_grade", map[string]any{"module_id": "x", "assessment": "Quiz"}), "")
	failsWith(t, call(t, s, "create_semester", map[string]any{"name": strings.Repeat("n", 101)}), "too long")
	failsWith(t, call(t, s, "create_semester", map[string]any{"name": "x", "start_date": "31/12/2026"}), "YYYY-MM-DD")
	if e.countSemesters("alice") != 0 {
		t.Fatal("invalid calls created data")
	}
	// Tools advertise strict schemas.
	res, _ := s.ListTools(context.Background(), nil)
	for _, tl := range res.Tools {
		if tl.Name == "get_grade" {
			b, _ := json.Marshal(tl.InputSchema)
			if !strings.Contains(string(b), `"additionalProperties":false`) || !strings.Contains(string(b), `"enum":["CC1","CC2","Lab","Exam"]`) {
				t.Fatalf("get_grade schema: %s", b)
			}
			if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
				t.Fatal("read tools must be annotated read-only")
			}
		}
		if tl.Name == "delete_semester" && (tl.Annotations == nil || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Fatal("delete tools must be annotated destructive")
		}
	}
}

// ---------- rate limiting ----------

func TestSlidingWindowHasNoBoundaryBurst(t *testing.T) {
	l := newSlidingLimiter(5 * time.Minute)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		if !l.take(t0.Add(4*time.Minute+59*time.Second), limitCheck{"k", 10}).Allowed {
			t.Fatal("first 10 should pass")
		}
	}
	// A fixed window would reset at 5:00; a sliding one still remembers the burst at 4:59.
	if l.take(t0.Add(5*time.Minute+time.Second), limitCheck{"k", 10}).Allowed {
		t.Fatal("boundary burst allowed")
	}
	if !l.take(t0.Add(9*time.Minute+59*time.Second+time.Millisecond), limitCheck{"k", 10}).Allowed {
		t.Fatal("slot should free once the burst leaves the window")
	}
	// One rejected limit means nothing is recorded anywhere.
	l2 := newSlidingLimiter(time.Minute)
	l2.take(t0, limitCheck{"user", 1})
	if l2.take(t0, limitCheck{"key", 5}, limitCheck{"user", 1}).Allowed || l2.count("key", t0) != 0 {
		t.Fatal("a rejected request must not consume the other limit")
	}
}

func TestRateLimitPerAPIKey(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "k", fullAccess)
	var last resp
	for i := 1; i <= 100; i++ {
		last = expect(t, e.ping(secret), 200)
	}
	if last.header.Get("X-RateLimit-Limit") != "100" || last.header.Get("X-RateLimit-Remaining") != "0" || last.header.Get("X-RateLimit-Reset") == "" {
		t.Fatalf("headers on the 100th request: %v", last.header)
	}
	// The 101st is rejected and does not run the tool.
	r := expect(t, e.rawMCP(secret, "tools/call", map[string]any{"name": "create_semester", "arguments": map[string]any{"name": "should not exist"}}), 429)
	if r.header.Get("Retry-After") == "" || !strings.Contains(string(r.body), "Rate limit exceeded") || !strings.Contains(string(r.body), `"jsonrpc":"2.0"`) {
		t.Fatalf("429 response: %v %s", r.header, r.body)
	}
	if e.countSemesters("alice") != 0 {
		t.Fatal("a rejected request executed")
	}
	e.now = e.now.Add(4 * time.Minute)
	expect(t, e.ping(secret), 429) // still inside the window
	e.now = e.now.Add(time.Minute + time.Second)
	expect(t, e.ping(secret), 200) // the window moved on
}

func TestRateLimitIsSharedAcrossAUsersCredentials(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, k1 := alice.createKey(t, "one", fullAccess)
	_, k2 := alice.createKey(t, "two", fullAccess)
	_, k3 := alice.createKey(t, "three", fullAccess)
	for i := 0; i < 100; i++ {
		expect(t, e.ping(k1), 200)
	}
	expect(t, e.ping(k1), 429) // key limit
	for i := 0; i < 50; i++ {
		expect(t, e.ping(k2), 200)
	}
	r := expect(t, e.ping(k3), 429) // user limit: 150 across all keys
	if r.header.Get("X-RateLimit-Limit") != "150" {
		t.Fatalf("the user limit should be reported, got %s", r.header.Get("X-RateLimit-Limit"))
	}
	// Another user is unaffected.
	bob := e.student(e.adminClient(), "bob")
	_, kb := bob.createKey(t, "b", fullAccess)
	expect(t, e.ping(kb), 200)
	ov := expect(t, alice.get("/api/integrations/overview"), 200).json()
	if ov["windowRequests"].(float64) != 150 || ov["totalRequests"].(float64) != 150 {
		t.Fatalf("overview counters: %v", ov)
	}
}

func TestRateLimitHoldsUnderConcurrency(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.CredentialRateLimit = 20; c.UserRateLimit = 30 })
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "k", fullAccess)
	var accepted, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch e.ping(secret).status {
			case 200:
				accepted.Add(1)
			case 429:
				rejected.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 20 || rejected.Load() != 40 {
		t.Fatalf("accepted %d, rejected %d; want exactly 20 / 40", accepted.Load(), rejected.Load())
	}
}

func TestRateLimitConfiguration(t *testing.T) {
	t.Setenv("GRADLY_API_KEY_RATE_LIMIT", "7")
	t.Setenv("GRADLY_USER_RATE_LIMIT", "9")
	t.Setenv("GRADLY_RATE_LIMIT_WINDOW", "90s")
	t.Setenv("GRADLY_ACTIVITY_RETENTION", "30d")
	c := config.FromEnv()
	if c.CredentialRateLimit != 7 || c.UserRateLimit != 9 || c.RateLimitWindow != 90*time.Second || c.ActivityRetention != 30*24*time.Hour {
		t.Fatalf("config: %+v", c)
	}
	t.Setenv("GRADLY_API_KEY_RATE_LIMIT", "-1")
	t.Setenv("GRADLY_RATE_LIMIT_WINDOW", "soon")
	c = config.FromEnv()
	if c.CredentialRateLimit != 100 || c.RateLimitWindow != 5*time.Minute {
		t.Fatalf("invalid values must fall back to defaults: %+v", c)
	}
}

// ---------- OAuth ----------

type oauthClient struct {
	id, redirect string
}

func (e *env) register(t *testing.T, body map[string]any) resp {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(e.srv.URL+"/oauth/register", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	raw, _ := json.Marshal(m)
	return resp{res.StatusCode, raw, res.Header}
}

func (e *env) registerClient(t *testing.T, redirect string) oauthClient {
	t.Helper()
	r := expect(t, e.register(t, map[string]any{"client_name": "Hermes", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"}), 201)
	return oauthClient{r.json()["client_id"].(string), redirect}
}

func (e *env) form(t *testing.T, path string, v url.Values) resp {
	t.Helper()
	res, err := http.PostForm(e.srv.URL+path, v)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	raw, _ := json.Marshal(m)
	return resp{res.StatusCode, raw, res.Header}
}

func pkce() (verifier, challenge string) {
	verifier = newToken() + "abc"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func authParams(cl oauthClient, challenge, scope string) url.Values {
	return url.Values{"response_type": {"code"}, "client_id": {cl.id}, "redirect_uri": {cl.redirect}, "state": {"xyz"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {scope}}
}

func approveBody(p url.Values, approve bool, scopes []string) map[string]any {
	return map[string]any{"responseType": p.Get("response_type"), "clientId": p.Get("client_id"), "redirectUri": p.Get("redirect_uri"),
		"state": p.Get("state"), "codeChallenge": p.Get("code_challenge"), "codeChallengeMethod": p.Get("code_challenge_method"),
		"scope": p.Get("scope"), "resource": p.Get("resource"), "approve": approve, "scopes": scopes}
}

// authorize runs the consent step as the signed-in user and returns the authorization code.
func (e *env) authorize(t *testing.T, user *client, cl oauthClient, scopes []string) (code, verifier string) {
	t.Helper()
	verifier, challenge := pkce()
	p := authParams(cl, challenge, strings.Join(scopes, " "))
	info := expect(t, user.get("/api/oauth/authorize?"+p.Encode()), 200).json()
	if info["ok"] != true {
		t.Fatalf("authorize info: %v", info)
	}
	r := expect(t, user.post("/api/oauth/authorize", approveBody(p, true, scopes)), 200)
	u, _ := url.Parse(r.json()["redirect"].(string))
	if u.Query().Get("state") != "xyz" || u.Query().Get("iss") != e.app.Cfg.BaseURL {
		t.Fatalf("redirect: %s", u)
	}
	return u.Query().Get("code"), verifier
}

func (e *env) exchange(t *testing.T, cl oauthClient, code, verifier string) resp {
	return e.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"redirect_uri": {cl.redirect}, "client_id": {cl.id}})
}

func (e *env) refreshTokens(t *testing.T, cl oauthClient, refresh string) resp {
	return e.form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {cl.id}})
}

func TestOAuthDiscoveryMetadata(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		m := expect(t, e.client().get(p), 200)
		if m.json()["resource"] != "http://gradly.test/mcp" || m.json()["authorization_servers"].([]any)[0] != "http://gradly.test" {
			t.Fatalf("%s: %s", p, m.body)
		}
		if m.header.Get("Access-Control-Allow-Origin") != "*" {
			t.Error("metadata must be readable cross-origin")
		}
	}
	m := expect(t, e.client().get("/.well-known/oauth-authorization-server"), 200).json()
	if m["issuer"] != "http://gradly.test" || m["code_challenge_methods_supported"].([]any)[0] != "S256" ||
		m["registration_endpoint"] != "http://gradly.test/oauth/register" || slices.Contains(m["grant_types_supported"].([]any), any("implicit")) {
		t.Fatalf("AS metadata: %v", m)
	}
}

func TestOAuthClientRegistrationValidation(t *testing.T) {
	e := newEnv(t)
	bad := [][]string{{}, {"http://evil.example/cb"}, {"javascript:alert(1)"}, {"https://app.example/cb#frag"}, {"https://user:pw@app.example/cb"},
		{"data:text/html,hi"}, {"https://a/1", "https://a/2", "https://a/3", "https://a/4", "https://a/5", "https://a/6"}}
	for _, uris := range bad {
		if r := e.register(t, map[string]any{"client_name": "x", "redirect_uris": uris}); r.status != 400 {
			t.Errorf("%v accepted (%d)", uris, r.status)
		}
	}
	expect(t, e.register(t, map[string]any{"redirect_uris": []string{"https://a.example/cb"}, "token_endpoint_auth_method": "client_secret_basic"}), 400)
	expect(t, e.register(t, map[string]any{"redirect_uris": []string{"https://a.example/cb"}, "grant_types": []string{"password"}}), 400)
	for _, good := range []string{"https://claude.ai/api/mcp/auth_callback", "http://127.0.0.1:33418/callback", "http://localhost/cb", "cursor://anysphere.cursor-mcp/oauth/callback"} {
		expect(t, e.register(t, map[string]any{"client_name": "ok", "redirect_uris": []string{good}}), 201)
	}
}

func TestOAuthFullFlowAndRefreshRotation(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	cl := e.registerClient(t, "http://127.0.0.1:8976/callback")

	code, verifier := e.authorize(t, alice, cl, []string{ScopeSemestersRead, ScopeSemestersWrite})
	tok := expect(t, e.exchange(t, cl, code, verifier), 200)
	if tok.header.Get("Cache-Control") != "no-store" {
		t.Error("token responses must not be cached")
	}
	tj := tok.json()
	access, refresh := tj["access_token"].(string), tj["refresh_token"].(string)
	if !strings.HasPrefix(access, accessTokenPrefix) || !strings.HasPrefix(refresh, refreshTokenPrefix) || tj["token_type"] != "Bearer" ||
		tj["expires_in"].(float64) != 3600 || tj["scope"] != "semesters:read semesters:write" {
		t.Fatalf("token response: %v", tj)
	}

	// The access token works on /mcp with exactly the granted scopes.
	s := e.mustSession(access)
	ok(t, call(t, s, "create_semester", map[string]any{"name": "Via OAuth"}))
	if slices.Contains(toolNames(t, s), "add_grade") {
		t.Fatal("ungranted tools offered")
	}
	// The refresh token is not an access token.
	expect(t, e.ping(refresh), 401)
	// The code is single-use, and replaying it revokes what it produced (the code must have leaked).
	expect(t, e.exchange(t, cl, code, verifier), 400)
	expect(t, e.ping(access), 401)
	code, verifier = e.authorize(t, alice, cl, []string{ScopeSemestersRead})
	tj = expect(t, e.exchange(t, cl, code, verifier), 200).json()
	access, refresh = tj["access_token"].(string), tj["refresh_token"].(string)

	// Refresh rotates: a new pair, and the old refresh token is spent.
	r2 := expect(t, e.refreshTokens(t, cl, refresh), 200).json()
	access2, refresh2 := r2["access_token"].(string), r2["refresh_token"].(string)
	if refresh2 == refresh || access2 == access {
		t.Fatal("refresh must rotate both tokens")
	}
	expect(t, e.ping(access2), 200)
	// Reusing a spent refresh token revokes the whole connection.
	expect(t, e.refreshTokens(t, cl, refresh), 400)
	expect(t, e.ping(access2), 401)
	expect(t, e.refreshTokens(t, cl, refresh2), 400)

	// Activity shows the OAuth application by name.
	acts := expect(t, alice.get("/api/integrations/activity"), 200).json()
	if acts["items"].([]any)[0].(map[string]any)["credentialName"] != "Hermes" {
		t.Fatalf("activity: %s", acts)
	}
}

func TestOAuthRejectsBadRequests(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	cl := e.registerClient(t, "https://app.example/cb")
	_, challenge := pkce()

	// Untrusted client or redirect URI: shown to the user, never redirected.
	for _, p := range []url.Values{
		{"response_type": {"code"}, "client_id": {"nope"}, "redirect_uri": {cl.redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}},
		{"response_type": {"code"}, "client_id": {cl.id}, "redirect_uri": {"https://evil.example/cb"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}},
		{"response_type": {"code"}, "client_id": {cl.id}, "redirect_uri": {"https://app.example/cb/../steal"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}},
	} {
		info := expect(t, alice.get("/api/oauth/authorize?"+p.Encode()), 200).json()
		if info["ok"] != false || info["redirect"] != nil {
			t.Fatalf("unsafe request %v answered %v", p, info)
		}
		expect(t, alice.post("/api/oauth/authorize", approveBody(p, true, fullAccess)), 400)
	}
	// Trusted redirect URI: errors go back to the client.
	for name, p := range map[string]url.Values{
		"no PKCE":    {"response_type": {"code"}, "client_id": {cl.id}, "redirect_uri": {cl.redirect}},
		"plain PKCE": {"response_type": {"code"}, "client_id": {cl.id}, "redirect_uri": {cl.redirect}, "code_challenge": {challenge}, "code_challenge_method": {"plain"}},
		"implicit":   {"response_type": {"token"}, "client_id": {cl.id}, "redirect_uri": {cl.redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}},
		"audience":   {"response_type": {"code"}, "client_id": {cl.id}, "redirect_uri": {cl.redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {"https://other.example/mcp"}},
	} {
		info := expect(t, alice.get("/api/oauth/authorize?"+p.Encode()), 200).json()
		if info["ok"] != false || !strings.HasPrefix(fmt.Sprint(info["redirect"]), cl.redirect+"?error=") {
			t.Fatalf("%s: %v", name, info)
		}
	}
	// Signed-out users can't approve anything.
	p := authParams(cl, challenge, "grades:read")
	expect(t, e.client().post("/api/oauth/authorize", approveBody(p, true, fullAccess)), 401)

	// Denying sends access_denied.
	r := expect(t, alice.post("/api/oauth/authorize", approveBody(p, false, nil)), 200).json()
	if !strings.Contains(r["redirect"].(string), "error=access_denied") || !strings.Contains(r["redirect"].(string), "state=xyz") {
		t.Fatalf("deny: %v", r)
	}
	// The user can't grant more than was requested.
	r = expect(t, alice.post("/api/oauth/authorize", approveBody(p, true, fullAccess)), 200).json()
	u, _ := url.Parse(r["redirect"].(string))
	var ac models.OAuthCode
	e.app.DB.First(&ac, "code_hash = ?", hashToken(u.Query().Get("code")))
	if ac.Scopes != "grades:read" {
		t.Fatalf("granted %q, want only what was requested", ac.Scopes)
	}

	// Token endpoint: a wrong verifier fails and burns the code.
	code, verifier := e.authorize(t, alice, cl, []string{ScopeGradesRead})
	wrong, _ := pkce()
	expect(t, e.exchange(t, cl, code, wrong), 400)
	expect(t, e.exchange(t, cl, code, verifier), 400)
	// Wrong redirect URI, wrong client, unsupported grant.
	code, verifier = e.authorize(t, alice, cl, []string{ScopeGradesRead})
	other := e.registerClient(t, "https://other.example/cb")
	expect(t, e.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {cl.redirect}, "client_id": {other.id}}), 400)
	code, verifier = e.authorize(t, alice, cl, []string{ScopeGradesRead})
	expect(t, e.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"https://app.example/other"}, "client_id": {cl.id}}), 400)
	expect(t, e.form(t, "/oauth/token", url.Values{"grant_type": {"password"}, "username": {"alice"}, "password": {"x"}, "client_id": {cl.id}}), 400)
	expect(t, e.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {"x"}, "code_verifier": {verifier}}), 401)
	// Codes expire.
	code, verifier = e.authorize(t, alice, cl, []string{ScopeGradesRead})
	e.now = e.now.Add(authCodeLifetime + time.Second)
	expect(t, e.exchange(t, cl, code, verifier), 400)
}

func TestOAuthTokenExpiryRevocationAndIsolation(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	alice, bob := e.student(admin, "alice"), e.student(admin, "bob")
	cl := e.registerClient(t, "http://localhost:9999/cb")
	code, verifier := e.authorize(t, alice, cl, fullAccess)
	tj := expect(t, e.exchange(t, cl, code, verifier), 200).json()
	access, refresh := tj["access_token"].(string), tj["refresh_token"].(string)

	// Access tokens expire after an hour; the refresh token still works.
	e.now = e.now.Add(accessTokenLifetime + time.Second)
	expect(t, e.ping(access), 401)

	// A second application and an API key stay unaffected by revoking the first.
	cl2 := e.registerClient(t, "https://second.example/cb")
	code2, verifier2 := e.authorize(t, alice, cl2, []string{ScopeGradesRead})
	access2 := expect(t, e.exchange(t, cl2, code2, verifier2), 200).json()["access_token"].(string)
	_, key := alice.createKey(t, "k", fullAccess)

	tj = expect(t, e.refreshTokens(t, cl, refresh), 200).json()
	access, refresh = tj["access_token"].(string), tj["refresh_token"].(string)
	expect(t, e.ping(access), 200)

	// The connected applications list, isolated per user.
	apps := expect(t, alice.get("/api/integrations/apps"), 200).json()["items"].([]any)
	if len(apps) != 2 {
		t.Fatalf("apps: %v", apps)
	}
	var grantID string
	for _, a := range apps {
		if a.(map[string]any)["clientId"] == cl.id {
			grantID = a.(map[string]any)["id"].(string)
			if a.(map[string]any)["clientName"] != "Hermes" || len(a.(map[string]any)["scopes"].([]any)) != len(fullAccess) {
				t.Fatalf("app: %v", a)
			}
		}
	}
	if n := len(expect(t, bob.get("/api/integrations/apps"), 200).json()["items"].([]any)); n != 0 {
		t.Fatal("bob sees alice's applications")
	}
	expect(t, bob.post("/api/integrations/apps/"+grantID+"/revoke", nil), 404)
	expect(t, e.ping(access), 200)

	// Revoking from the dashboard is immediate, for access and refresh tokens alike.
	expect(t, alice.del("/api/integrations/apps/"+grantID), 400)
	expect(t, alice.post("/api/integrations/apps/"+grantID+"/revoke", nil), 200)
	expect(t, e.ping(access), 401)
	expect(t, e.refreshTokens(t, cl, refresh), 400)
	expect(t, e.ping(access2), 200)
	expect(t, e.ping(key), 200)
	expect(t, alice.del("/api/integrations/apps/"+grantID), 200)

	// RFC 7009 revocation by the client: refresh token ends that connection.
	code2, verifier2 = e.authorize(t, alice, cl2, []string{ScopeGradesRead})
	tj = expect(t, e.exchange(t, cl2, code2, verifier2), 200).json()
	expect(t, e.form(t, "/oauth/revoke", url.Values{"token": {tj["refresh_token"].(string)}, "client_id": {"someone-else"}}), 200)
	expect(t, e.ping(tj["access_token"].(string)), 200) // another client can't revoke it
	expect(t, e.form(t, "/oauth/revoke", url.Values{"token": {tj["refresh_token"].(string)}, "client_id": {cl2.id}}), 200)
	expect(t, e.ping(tj["access_token"].(string)), 401)
	expect(t, e.form(t, "/oauth/revoke", url.Values{"token": {"garbage"}, "client_id": {cl2.id}}), 200)
}

func TestOAuthRequestsAreRateLimited(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.CredentialRateLimit = 5; c.UserRateLimit = 8 })
	alice := e.student(e.adminClient(), "alice")
	cl := e.registerClient(t, "https://app.example/cb")
	code, verifier := e.authorize(t, alice, cl, fullAccess)
	access := expect(t, e.exchange(t, cl, code, verifier), 200).json()["access_token"].(string)
	_, key := alice.createKey(t, "k", fullAccess)
	for i := 0; i < 5; i++ {
		expect(t, e.ping(access), 200)
	}
	expect(t, e.ping(access), 429) // per-connection limit
	for i := 0; i < 3; i++ {
		expect(t, e.ping(key), 200)
	}
	expect(t, e.ping(key), 429) // OAuth requests count toward the shared user limit
}

// ---------- activity log ----------

func TestActivityLog(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.CredentialRateLimit = 4 })
	admin := e.adminClient()
	alice, bob := e.student(admin, "alice"), e.student(admin, "bob")
	keyID, secret := alice.createKey(t, "Personal script", fullAccess)
	_, roSecret := alice.createKey(t, "Reader", readScopes())
	s := e.mustSession(secret)
	ok(t, call(t, s, "create_semester", map[string]any{"name": "S"}))
	failsWith(t, call(t, s, "get_semester", map[string]any{"semester_id": "missing"}), "not found")
	for e.ping(secret).status == 200 {
	}
	ro := e.mustSession(roSecret)
	ok(t, call(t, ro, "list_semesters", nil))

	all := expect(t, alice.get("/api/integrations/activity"), 200).json()
	items := all["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("events: %s", all)
	}
	byOp := map[string]map[string]any{}
	for _, it := range items {
		byOp[it.(map[string]any)["operation"].(string)] = it.(map[string]any)
	}
	if ev := byOp["create_semester"]; ev["success"] != true || ev["category"] != "write" || ev["credentialName"] != "Personal script" || ev["status"].(float64) != 200 {
		t.Fatalf("create event: %v", ev)
	}
	if ev := byOp["get_semester"]; ev["success"] != false || ev["errorCode"] != "not_found" || ev["status"].(float64) != 404 {
		t.Fatalf("failed event: %v", ev)
	}
	if ev := byOp["rate_limited"]; ev["status"].(float64) != 429 || ev["category"] != "limit" {
		t.Fatalf("rate-limit event: %v", ev)
	}

	// Filters and pagination.
	if n := expect(t, alice.get("/api/integrations/activity?status=failure"), 200).json()["total"].(float64); n != 2 {
		t.Fatalf("failures = %v", n)
	}
	if n := expect(t, alice.get("/api/integrations/activity?credential="+keyID), 200).json()["total"].(float64); n != 3 {
		t.Fatalf("by credential = %v", n)
	}
	if n := expect(t, alice.get("/api/integrations/activity?operation=list_semesters"), 200).json()["total"].(float64); n != 1 {
		t.Fatalf("by operation = %v", n)
	}
	page := expect(t, alice.get("/api/integrations/activity?page=2&pageSize=3"), 200).json()
	if len(page["items"].([]any)) != 1 || page["total"].(float64) != 4 {
		t.Fatalf("page 2: %v", page)
	}

	// Isolation, and nothing secret or academic is stored.
	if n := expect(t, bob.get("/api/integrations/activity"), 200).json()["total"].(float64); n != 0 {
		t.Fatal("bob sees alice's activity")
	}
	var rows []models.IntegrationEvent
	e.app.DB.Find(&rows)
	raw, _ := json.Marshal(rows)
	for _, leak := range []string{secret, roSecret, "Bearer", `"S"`} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("activity log contains %q", leak)
		}
	}

	ov := expect(t, alice.get("/api/integrations/overview"), 200).json()
	if ov["activeKeys"].(float64) != 2 || len(ov["recent"].([]any)) != 4 || ov["failuresLast24h"].(float64) != 2 {
		t.Fatalf("overview: %v", ov)
	}
}

// The integration tables are additive and can be dropped to roll the feature back (docs/integrations.md lists them).
func TestIntegrationTablesAreAdditiveAndDroppable(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	semID := alice.newSemester(t, "Keep me")
	alice.createKey(t, "k", fullAccess)
	m := e.app.DB.Migrator()
	for _, name := range []string{"api_keys", "o_auth_clients", "o_auth_grants", "o_auth_codes", "o_auth_tokens", "integration_events", "idempotency_records"} {
		if !m.HasTable(name) {
			t.Fatalf("missing table %s", name)
		}
	}
	for _, tbl := range models.IntegrationTables() {
		if err := m.DropTable(tbl); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, alice.get("/api/semesters/"+semID), 200) // academic data and accounts are untouched
	if m.HasTable("api_keys") {
		t.Fatal("rollback left integration tables behind")
	}
}

func TestActivityRetention(t *testing.T) {
	e := newEnv(t)
	alice := e.student(e.adminClient(), "alice")
	_, secret := alice.createKey(t, "k", fullAccess)
	p, err := e.app.authenticate(secret)
	if err != nil {
		t.Fatal(err)
	}
	e.app.logEvent(p, "old", "read", true, 200, "", 0)
	e.now = e.now.Add(e.app.Cfg.ActivityRetention + time.Hour)
	e.app.logEvent(p, "new", "read", true, 200, "", 0)
	e.app.pruneEvents(p.User.ID)
	var ops []string
	e.app.DB.Model(&models.IntegrationEvent{}).Pluck("operation", &ops)
	if !reflect.DeepEqual(ops, []string{"new"}) {
		t.Fatalf("after retention: %v", ops)
	}
}

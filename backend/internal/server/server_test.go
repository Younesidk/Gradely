package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/younesidk/gradely/internal/config"
	"github.com/younesidk/gradely/internal/mail"
	"github.com/younesidk/gradely/internal/models"
	"github.com/younesidk/gradely/internal/web"
	"golang.org/x/crypto/bcrypt"
)

// ---------- harness ----------

type fakeMail struct {
	mu   sync.Mutex
	on   bool
	sent []mail.Message
}

func (f *fakeMail) Configured() bool { return f.on }
func (f *fakeMail) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

type env struct {
	t    *testing.T
	app  *App
	srv  *httptest.Server
	mail *fakeMail
	now  time.Time
}

const adminPassword = "Admin-passw0rd-long"

func newEnv(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cfg := config.Config{
		DataDir: dir, BaseURL: "http://gradly.test", AuthRequestsPerMin: 1000, InvitationValidDays: 7,
		AdminEmail: "admin@example.com", AdminUsername: "admin", AdminPassword: adminPassword,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	fm := &fakeMail{}
	app := New(cfg, db, fm)
	app.BcryptCost = bcrypt.MinCost
	e := &env{t: t, app: app, mail: fm, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	app.Now = func() time.Time { return e.now }
	if err := app.BootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(app.Router())
	t.Cleanup(e.srv.Close)
	return e
}

type client struct {
	e   *env
	jar *cookiejar.Jar
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, jar: jar}
}

type resp struct {
	status int
	body   []byte
	header http.Header
}

func (r resp) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (r resp) errText() string { s, _ := r.json()["error"].(string); return s }

func (c *client) do(method, path string, body any) resp {
	c.e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gradly", "1")
	hc := &http.Client{Jar: c.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(res.Body)
	return resp{res.StatusCode, buf.Bytes(), res.Header}
}

func (c *client) get(path string) resp            { return c.do("GET", path, nil) }
func (c *client) post(path string, body any) resp { return c.do("POST", path, body) }
func (c *client) put(path string, body any) resp  { return c.do("PUT", path, body) }
func (c *client) del(path string) resp            { return c.do("DELETE", path, nil) }
func expect(t *testing.T, r resp, status int) resp {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d (body %s)", r.status, status, r.body)
	}
	return r
}

func (c *client) login(identifier, password string) resp {
	return c.post("/api/auth/login", map[string]any{"identifier": identifier, "password": password})
}

func (e *env) adminClient() *client {
	c := e.client()
	expect(e.t, c.login("admin", adminPassword), 200)
	return c
}

// invite creates an invitation as admin and returns the raw token.
func (e *env) invite(admin *client, email string) string {
	r := expect(e.t, admin.post("/api/admin/invitations", map[string]any{"email": email}), 201)
	link := r.json()["url"].(string)
	u, _ := url.Parse(link)
	return u.Query().Get("token")
}

// student registers a new student through an invitation and returns a signed-in client.
func (e *env) student(admin *client, name string) *client {
	token := e.invite(admin, name+"@example.com")
	c := e.client()
	expect(e.t, c.post("/api/auth/register", map[string]any{"token": token, "userName": name, "password": "Student-passw0rd"}), 200)
	return c
}

func id(r resp) string { s, _ := r.json()["id"].(string); return s }

func num(v any) float64 { f, _ := v.(float64); return f }

// ---------- auth ----------

func TestBootstrapCreatesAdminOnce(t *testing.T) {
	e := newEnv(t)
	admin := e.client()
	r := expect(t, admin.login("admin@example.com", adminPassword), 200) // email works too
	if r.json()["isAdmin"] != true {
		t.Error("bootstrapped user should be an administrator")
	}
	// Changing the env afterwards must not create or overwrite anything.
	e.app.Cfg.AdminPassword = "Different-passw0rd"
	if err := e.app.BootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	expect(t, e.client().login("admin", adminPassword), 200)
	expect(t, e.client().login("admin", "Different-passw0rd"), 401)
}

func TestBootstrapStarterFormula(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.DefaultLabFormula = "(CC1 + CC2) / 2 * 0.2 + Lab * 0.2 + Exam * 0.6" })
	admin := e.adminClient()
	list := admin.get("/api/formulas").json()["formulas"].([]any)
	if len(list) != 2 {
		t.Fatalf("admin should start with 2 formulas, got %d", len(list))
	}
}

func TestLoginRules(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	bad := expect(t, c.login("admin", "wrong-password"), 401)
	unknown := expect(t, c.login("nobody", "whatever"), 401)
	if bad.errText() != unknown.errText() || bad.errText() == "" {
		t.Error("unknown user and wrong password must look identical")
	}
	if r := c.get("/api/auth/me"); r.status != 401 {
		t.Errorf("not signed in: %d", r.status)
	}
	expect(t, c.login("ADMIN", adminPassword), 200) // case-insensitive
	expect(t, c.get("/api/auth/me"), 200)
	expect(t, c.post("/api/auth/logout", nil), 200)
	expect(t, c.get("/api/auth/me"), 401)
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for i := 0; i < maxFailedLogins; i++ {
		expect(t, c.login("admin", "wrong-password"), 401)
	}
	expect(t, c.login("admin", adminPassword), 401) // locked, even with the right password
	e.now = e.now.Add(lockoutDuration + time.Minute)
	expect(t, c.login("admin", adminPassword), 200)
}

func TestMutatingRequestsNeedTheCustomHeader(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/auth/login", strings.NewReader(`{"identifier":"admin","password":"`+adminPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 400 {
		t.Errorf("a cross-site style request must be rejected, got %d", res.StatusCode)
	}
	if len(res.Cookies()) != 0 {
		t.Error("no session may be issued")
	}
}

func TestSessionCookieFlags(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SecureCookies = true })
	r := expect(t, e.client().login("admin", adminPassword), 200)
	raw := r.header.Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(raw, want) {
			t.Errorf("cookie %q is missing %s", raw, want)
		}
	}
}

func TestAuthRateLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AuthRequestsPerMin = 3 })
	c := e.client()
	var last resp
	for i := 0; i < 5; i++ {
		last = c.login("admin", "wrong-password")
	}
	if last.status != 429 {
		t.Errorf("the 5th attempt should be rate limited, got %d", last.status)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	r := e.client().get("/api/auth/me")
	if r.header.Get("X-Frame-Options") != "DENY" || r.header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(r.header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("missing security headers: %v", r.header)
	}
}

// ---------- invitations ----------

func TestInvitationLifecycle(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	token := e.invite(admin, "sara@example.com")

	// Stored only as a hash.
	var inv models.Invitation
	e.app.DB.First(&inv)
	if inv.TokenHash == token || inv.TokenHash != hashToken(token) {
		t.Error("only the token hash may be stored")
	}

	info := expect(t, e.client().get("/api/auth/invitation?token="+token), 200)
	if info.json()["email"] != "sara@example.com" {
		t.Errorf("invited email = %v", info.json())
	}
	expect(t, e.client().get("/api/auth/invitation?token=nope"), 404)

	c := e.client()
	weak := c.post("/api/auth/register", map[string]any{"token": token, "userName": "sara", "password": "short"})
	expect(t, weak, 400)
	// The failed attempt must not burn the invitation.
	expect(t, c.post("/api/auth/register", map[string]any{"token": token, "userName": "sara", "password": "Sara-passw0rd"}), 200)
	expect(t, c.get("/api/auth/me"), 200) // registration signs in

	// Single use.
	other := e.client()
	expect(t, other.post("/api/auth/register", map[string]any{"token": token, "userName": "sara2", "password": "Sara-passw0rd"}), 400)

	// The new account got the starter formula.
	list := c.get("/api/formulas").json()["formulas"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != defaultNoLabName {
		t.Errorf("starter formulas = %v", list)
	}
}

func TestInvitationExpiryRevokeRegenerate(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()

	token := e.invite(admin, "late@example.com")
	e.now = e.now.Add(8 * 24 * time.Hour)
	admin = e.adminClient()                                             // the earlier session expired with the clock
	expect(t, e.client().get("/api/auth/invitation?token="+token), 404) // expired

	rows := admin.get("/api/admin/invitations").json()["items"].([]any)
	invID := rows[0].(map[string]any)["id"].(string)
	if rows[0].(map[string]any)["status"] != "Expired" {
		t.Errorf("status = %v", rows[0])
	}
	fresh := expect(t, admin.post("/api/admin/invitations/"+invID+"/regenerate", nil), 200).json()["url"].(string)
	u, _ := url.Parse(fresh)
	newToken := u.Query().Get("token")
	expect(t, e.client().get("/api/auth/invitation?token="+newToken), 200)
	expect(t, e.client().get("/api/auth/invitation?token="+token), 404) // old link is dead

	expect(t, admin.post("/api/admin/invitations/"+invID+"/revoke", nil), 200)
	expect(t, e.client().get("/api/auth/invitation?token="+newToken), 404)
	expect(t, admin.post("/api/admin/invitations/"+invID+"/revoke", nil), 400)
	expect(t, admin.post("/api/admin/invitations/"+invID+"/regenerate", nil), 400) // revoked
}

func TestInvitationValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	expect(t, admin.post("/api/admin/invitations", map[string]any{"email": "not-an-email"}), 400)
	expect(t, admin.post("/api/admin/invitations", map[string]any{"email": "admin@example.com"}), 400) // existing account
	e.invite(admin, "dup@example.com")
	expect(t, admin.post("/api/admin/invitations", map[string]any{"email": "DUP@example.com"}), 400) // pending
}

func TestInvitationEmailIsSentWhenConfigured(t *testing.T) {
	e := newEnv(t)
	e.mail.on = true
	admin := e.adminClient()
	r := expect(t, admin.post("/api/admin/invitations", map[string]any{"email": "mail@example.com"}), 201)
	if r.json()["emailSent"] != true || len(e.mail.sent) != 1 {
		t.Fatalf("email not sent: %v", r.json())
	}
	if !strings.Contains(e.mail.sent[0].Text, "Your grades. Your rules.") || !strings.Contains(e.mail.sent[0].Text, r.json()["url"].(string)) {
		t.Errorf("unexpected message: %+v", e.mail.sent[0])
	}
}

func TestRegistrationPasswordAndUsernameRules(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	cases := []struct{ user, pw string }{
		{"ab", "Valid-passw0rd"}, {"has space", "Valid-passw0rd"}, {"okname", "nodigitsatall"}, {"okname", "NOLOWER1234"}, {"admin", "Valid-passw0rd"},
	}
	token := e.invite(admin, "rules@example.com")
	for _, c := range cases {
		expect(t, e.client().post("/api/auth/register", map[string]any{"token": token, "userName": c.user, "password": c.pw}), 400)
	}
	expect(t, e.client().post("/api/auth/register", map[string]any{"token": token, "userName": "valid_name", "password": "Valid-passw0rd"}), 200)
}

// ---------- academic data ----------

func (c *client) newSemester(t *testing.T, name string) string {
	return id(expect(t, c.post("/api/semesters", map[string]any{"name": name}), 201))
}

func moduleBody(name string) map[string]any {
	return map[string]any{"name": name, "coefficient": 3, "maxGrade": 20, "passingThreshold": 10, "hasLabExam": false}
}

func TestGradeCalculationEndToEnd(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient()
	sem := c.newSemester(t, "S5")
	modID := id(expect(t, c.post("/api/semesters/"+sem+"/modules", moduleBody("Databases")), 201))

	view := func() map[string]any {
		return c.get("/api/semesters/" + sem).json()["view"].(map[string]any)
	}
	m := view()["modules"].([]any)[0].(map[string]any)
	if m["result"].(map[string]any)["status"] != "waitingForGrades" {
		t.Errorf("fresh module: %v", m["result"])
	}
	if m["formula"] == nil {
		t.Error("the user's default formula should be picked automatically")
	}

	expect(t, c.put("/api/modules/"+modID+"/grades", map[string]any{"cc1": 12, "cc2": 14}), 200)
	res := view()["modules"].([]any)[0].(map[string]any)["result"].(map[string]any)
	req := res["requiredExam"].(map[string]any)
	if res["status"] != "incomplete" || req["kind"] != "needed" || num(req["value"]) != 8 {
		t.Errorf("required exam: %v", res)
	}

	expect(t, c.put("/api/modules/"+modID+"/grades", map[string]any{"cc1": 12, "cc2": 14, "exam": 13}), 200)
	v := view()
	res = v["modules"].([]any)[0].(map[string]any)["result"].(map[string]any)
	if res["status"] != "complete" || num(res["grade"]) != 13 || res["passed"] != true {
		t.Errorf("complete: %v", res)
	}
	if num(v["summary"].(map[string]any)["average"]) != 13 {
		t.Errorf("summary: %v", v["summary"])
	}

	// Dashboard
	d := c.get("/api/dashboard").json()
	if num(d["overall"].(map[string]any)["average"]) != 13 || d["current"] == nil {
		t.Errorf("dashboard: %v", d)
	}
}

func TestGradeAndModuleValidation(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient()
	sem := c.newSemester(t, "S1")
	modID := id(expect(t, c.post("/api/semesters/"+sem+"/modules", moduleBody("M")), 201))

	expect(t, c.put("/api/modules/"+modID+"/grades", map[string]any{"cc1": 21}), 400)
	expect(t, c.put("/api/modules/"+modID+"/grades", map[string]any{"cc1": -1}), 400)
	expect(t, c.put("/api/modules/"+modID+"/grades", map[string]any{"cc1": 0, "cc2": 20}), 200) // 0 and max are valid

	bad := moduleBody("x")
	bad["passingThreshold"] = 25
	expect(t, c.post("/api/semesters/"+sem+"/modules", bad), 400)
	bad = moduleBody("")
	expect(t, c.post("/api/semesters/"+sem+"/modules", bad), 400)
	bad = moduleBody("x")
	bad["coefficient"] = -1
	expect(t, c.post("/api/semesters/"+sem+"/modules", bad), 400)

	// Lowering the maximum below an entered grade is refused.
	shrink := moduleBody("M")
	shrink["maxGrade"] = 10
	shrink["passingThreshold"] = 5
	expect(t, c.put("/api/modules/"+modID, shrink), 400)

	expect(t, c.post("/api/semesters", map[string]any{"name": " "}), 400)
	expect(t, c.post("/api/semesters", map[string]any{"name": "S", "startDate": "2027-02-01", "endDate": "2027-01-01"}), 400)
	expect(t, c.post("/api/semesters", map[string]any{"name": "S", "startDate": "nonsense"}), 400)
}

func TestFormulaKindMustMatchModuleKind(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient()
	sem := c.newSemester(t, "S1")
	labFormula := id(expect(t, c.post("/api/formulas", map[string]any{
		"name": "Lab", "expression": "(CC1 + CC2) / 2 * 0.2 + Lab * 0.2 + Exam * 0.6", "forLabExam": true}), 201))
	noLab := moduleBody("Plain")
	noLab["formulaId"] = labFormula
	expect(t, c.post("/api/semesters/"+sem+"/modules", noLab), 400)

	lab := moduleBody("With lab")
	lab["hasLabExam"] = true
	lab["formulaId"] = labFormula
	modID := id(expect(t, c.post("/api/semesters/"+sem+"/modules", lab), 201))
	expect(t, c.put("/api/modules/"+modID+"/grades", map[string]any{"cc1": 12, "cc2": 14, "lab": 16}), 200)
	res := c.get("/api/semesters/" + sem).json()["view"].(map[string]any)["modules"].([]any)[0].(map[string]any)["result"].(map[string]any)
	if num(res["requiredExam"].(map[string]any)["value"]) != 7 {
		t.Errorf("lab required exam: %v", res)
	}
}

func TestReorderAndDelete(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient()
	s1, s2, s3 := c.newSemester(t, "A"), c.newSemester(t, "B"), c.newSemester(t, "C")
	names := func() string {
		var out []string
		for _, s := range c.get("/api/semesters").body2slice() {
			out = append(out, s.(map[string]any)["semester"].(map[string]any)["name"].(string))
		}
		return strings.Join(out, "")
	}
	if names() != "ABC" {
		t.Fatalf("initial order %s", names())
	}
	expect(t, c.post("/api/semesters/"+s3+"/move", map[string]any{"direction": -1}), 200)
	if names() != "ACB" {
		t.Errorf("after move up: %s", names())
	}
	expect(t, c.post("/api/semesters/"+s1+"/move", map[string]any{"direction": -1}), 200) // first: no-op
	if names() != "ACB" {
		t.Errorf("after no-op: %s", names())
	}

	m1 := id(expect(t, c.post("/api/semesters/"+s2+"/modules", moduleBody("Zed")), 201))
	m2 := id(expect(t, c.post("/api/semesters/"+s2+"/modules", moduleBody("Alpha")), 201))
	expect(t, c.post("/api/modules/"+m2+"/move", map[string]any{"direction": -1}), 200)
	mods := c.get("/api/semesters/" + s2).json()["view"].(map[string]any)["modules"].([]any)
	if mods[0].(map[string]any)["module"].(map[string]any)["id"] != m2 {
		t.Error("module should have moved up")
	}
	expect(t, c.del("/api/modules/"+m1), 200)
	expect(t, c.del("/api/semesters/"+s2), 200)
	expect(t, c.get("/api/semesters/"+s2), 404)
	expect(t, c.get("/api/semesters/"+s2), 404)
	var left int64
	e.app.DB.Model(&models.Module{}).Count(&left)
	if left != 0 {
		t.Errorf("modules should cascade, %d left", left)
	}
}

func (r resp) body2slice() []any {
	var s []any
	_ = json.Unmarshal(r.body, &s)
	return s
}

// ---------- isolation ----------

func TestUsersCannotSeeOrChangeEachOther(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	alice, bob := e.student(admin, "alice"), e.student(admin, "bobby")

	sem := alice.newSemester(t, "Alice S1")
	mod := id(expect(t, alice.post("/api/semesters/"+sem+"/modules", moduleBody("Secret")), 201))
	formulas := alice.get("/api/formulas").json()["formulas"].([]any)
	aliceFormula := formulas[0].(map[string]any)["id"].(string)

	// Bob sees nothing of Alice's.
	if got := bob.get("/api/semesters").body2slice(); len(got) != 0 {
		t.Errorf("bob sees %d semesters", len(got))
	}
	for _, r := range []resp{
		bob.get("/api/semesters/" + sem),
		bob.put("/api/semesters/"+sem, map[string]any{"name": "pwned"}),
		bob.del("/api/semesters/" + sem),
		bob.post("/api/semesters/"+sem+"/modules", moduleBody("injected")),
		bob.put("/api/modules/"+mod+"/grades", map[string]any{"cc1": 20}),
		bob.put("/api/modules/"+mod, moduleBody("renamed")),
		bob.put("/api/modules/"+mod+"/formula", map[string]any{"formulaId": nil}),
		bob.del("/api/modules/" + mod),
		bob.put("/api/formulas/"+aliceFormula, map[string]any{"name": "x", "expression": "CC1"}),
		bob.post("/api/formulas/"+aliceFormula+"/default", nil),
		bob.del("/api/formulas/" + aliceFormula),
	} {
		if r.status != 404 {
			t.Errorf("cross-user access answered %d, want 404: %s", r.status, r.body)
		}
	}

	// Bob can't use Alice's formula on his own module.
	bobSem := bob.newSemester(t, "Bob S1")
	body := moduleBody("mine")
	body["formulaId"] = aliceFormula
	expect(t, bob.post("/api/semesters/"+bobSem+"/modules", body), 400)

	// Alice's data is untouched.
	v := alice.get("/api/semesters/" + sem).json()["view"].(map[string]any)
	if v["semester"].(map[string]any)["name"] != "Alice S1" || len(v["modules"].([]any)) != 1 {
		t.Error("alice's data changed")
	}
}

func TestAnonymousAndNonAdminAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	student := e.student(admin, "stud")
	anon := e.client()
	for _, p := range []string{"/api/dashboard", "/api/semesters", "/api/formulas", "/api/account", "/api/admin/overview", "/api/admin/users"} {
		if r := anon.get(p); r.status != 401 {
			t.Errorf("anonymous %s = %d", p, r.status)
		}
	}
	for _, p := range []string{"/api/admin/overview", "/api/admin/users", "/api/admin/invitations"} {
		if r := student.get(p); r.status != 403 {
			t.Errorf("student %s = %d, want 403", p, r.status)
		}
	}
	expect(t, student.post("/api/admin/invitations", map[string]any{"email": "x@example.com"}), 403)
	expect(t, student.put("/api/admin/default-lab-formula", map[string]any{"expression": "Lab"}), 403)
}

// ---------- formulas ----------

func TestFormulaRules(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient()
	create := func(name, expr string, lab, def bool) resp {
		return c.post("/api/formulas", map[string]any{"name": name, "expression": expr, "forLabExam": lab, "isDefault": def})
	}
	expect(t, create("", "CC1", false, false), 400)
	expect(t, create("Bad", "CC1 +", false, false), 400)
	expect(t, create("Const", "12", false, false), 400)        // must use a variable
	expect(t, create("NoLab", "CC1 + Lab", false, false), 400) // Lab needs a lab formula
	expect(t, create(strings.Repeat("x", 81), "CC1", false, false), 400)
	expect(t, create(defaultNoLabName, "CC1", false, false), 400) // duplicate name
	expect(t, create(strings.ToUpper(defaultNoLabName), "CC1", false, false), 400)

	heavy := id(expect(t, create("Heavy exam", "CC1 * 0.2 + Exam * 0.8", false, true), 201))
	// Making it the default clears the previous default of the same kind.
	var defaults int64
	e.app.DB.Model(&models.Formula{}).Where("for_lab_exam = ? AND is_default = ?", false, true).Count(&defaults)
	if defaults != 1 {
		t.Errorf("exactly one default per kind, got %d", defaults)
	}
	// First formula of the other kind becomes its default automatically.
	lab := id(expect(t, create("Lab one", "Lab + Exam", true, false), 201))
	var labDefault models.Formula
	e.app.DB.First(&labDefault, "id = ?", lab)
	if !labDefault.IsDefault {
		t.Error("the first formula of a kind should be its default")
	}

	// In-use formulas can't be deleted or change kind.
	sem := c.newSemester(t, "S")
	mod := moduleBody("Uses it")
	mod["formulaId"] = heavy
	expect(t, c.post("/api/semesters/"+sem+"/modules", mod), 201)
	r := c.del("/api/formulas/" + heavy)
	expect(t, r, 400)
	if !strings.Contains(r.errText(), "used by 1 module") {
		t.Errorf("message: %s", r.errText())
	}
	expect(t, c.put("/api/formulas/"+heavy, map[string]any{"name": "Heavy exam", "expression": "CC1 + Lab", "forLabExam": true}), 400)
	expect(t, c.put("/api/formulas/"+heavy, map[string]any{"name": "Heavy exam v2", "expression": "CC1 * 0.1 + Exam * 0.9", "forLabExam": false}), 200)

	std := c.get("/api/formulas").json()["formulas"].([]any)
	var stdID string
	for _, f := range std {
		if f.(map[string]any)["name"] == defaultNoLabName {
			stdID = f.(map[string]any)["id"].(string)
		}
	}
	expect(t, c.post("/api/formulas/"+stdID+"/default", nil), 200)
	expect(t, c.del("/api/formulas/"+lab), 200)
}

func TestFormulaPreview(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient()
	bad := expect(t, c.post("/api/formulas/preview", map[string]any{"expression": "CC1 +", "forLabExam": false}), 200).json()
	if bad["success"] != false || len(bad["errors"].([]any)) == 0 {
		t.Errorf("bad preview: %v", bad)
	}
	good := expect(t, c.post("/api/formulas/preview", map[string]any{
		"expression": "(CC1 + CC2) / 2 * 0.4 + Exam * 0.6", "cc1": 12, "cc2": 14, "exam": 13}), 200).json()
	if good["success"] != true || num(good["full"].(map[string]any)["grade"]) != 13 {
		t.Errorf("good preview: %v", good)
	}
	if num(good["required"].(map[string]any)["requiredExam"].(map[string]any)["value"]) != 8 {
		t.Errorf("required: %v", good["required"])
	}
}

func TestPlatformLabFormula(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	expect(t, admin.put("/api/admin/default-lab-formula", map[string]any{"expression": "Lab +"}), 400)
	expect(t, admin.put("/api/admin/default-lab-formula", map[string]any{"expression": "(CC1 + CC2) / 2 * 0.2 + Lab * 0.2 + Exam * 0.6"}), 200)

	// A user created before the default existed can add it; new users get it automatically.
	late := e.student(admin, "late")
	if got := late.get("/api/formulas").json()["formulas"].([]any); len(got) != 2 {
		t.Errorf("new users get the platform lab formula: %d", len(got))
	}
	expect(t, admin.post("/api/formulas/platform-lab", nil), 201)
	expect(t, admin.post("/api/formulas/platform-lab", nil), 400) // already has it
	expect(t, admin.put("/api/admin/default-lab-formula", map[string]any{"expression": ""}), 200)
	if admin.get("/api/formulas").json()["platformLab"] != nil {
		t.Error("platform lab formula should be cleared")
	}
}

// ---------- account ----------

func TestChangePasswordSignsOutEverywhere(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	stud := e.student(admin, "pwuser")
	second := e.client()
	expect(t, second.login("pwuser", "Student-passw0rd"), 200)

	expect(t, stud.put("/api/account/password", map[string]any{"current": "wrong", "next": "Another-passw0rd"}), 400)
	expect(t, stud.put("/api/account/password", map[string]any{"current": "Student-passw0rd", "next": "weak"}), 400)
	expect(t, stud.put("/api/account/password", map[string]any{"current": "Student-passw0rd", "next": "Another-passw0rd"}), 200)

	expect(t, stud.get("/api/auth/me"), 401)
	expect(t, second.get("/api/auth/me"), 401) // other devices are signed out too
	expect(t, e.client().login("pwuser", "Student-passw0rd"), 401)
	expect(t, e.client().login("pwuser", "Another-passw0rd"), 200)
}

func TestChangeUserName(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	stud := e.student(admin, "oldname")
	expect(t, stud.put("/api/account/username", map[string]any{"userName": "admin"}), 400) // taken
	expect(t, stud.put("/api/account/username", map[string]any{"userName": "x"}), 400)
	expect(t, stud.put("/api/account/username", map[string]any{"userName": "newname"}), 200)
	if stud.get("/api/auth/me").json()["userName"] != "newname" {
		t.Error("username not updated")
	}
	expect(t, e.client().login("newname", "Student-passw0rd"), 200)
}

func TestEmailChangeFlow(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	stud := e.student(admin, "mailer")

	expect(t, stud.post("/api/account/email-change", map[string]any{"email": "nope"}), 400)
	expect(t, stud.post("/api/account/email-change", map[string]any{"email": "mailer@example.com"}), 400) // same address

	// Delivery off: the link comes back for manual use.
	r := expect(t, stud.post("/api/account/email-change", map[string]any{"email": "new@example.com"}), 200).json()
	link := r["manualLink"].(string)
	u, _ := url.Parse(link)
	token := u.Query().Get("token")
	if r["emailSent"] != false || token == "" {
		t.Fatalf("manual link expected: %v", r)
	}
	if stud.get("/api/account").json()["email"] != "mailer@example.com" {
		t.Error("the address must not change before confirmation")
	}
	expect(t, e.client().post("/api/auth/confirm-email-change", map[string]any{"token": "bogus"}), 400)
	expect(t, e.client().post("/api/auth/confirm-email-change", map[string]any{"token": token}), 200)
	if stud.get("/api/account").json()["email"] != "new@example.com" {
		t.Error("email should have changed")
	}
	expect(t, e.client().post("/api/auth/confirm-email-change", map[string]any{"token": token}), 400) // single use

	// With delivery on, the message goes to the NEW address.
	e.mail.on = true
	r = expect(t, stud.post("/api/account/email-change", map[string]any{"email": "third@example.com"}), 200).json()
	if r["emailSent"] != true || len(e.mail.sent) != 1 || e.mail.sent[0].To != "third@example.com" {
		t.Errorf("mail: %v %+v", r, e.mail.sent)
	}
}

// ---------- administration ----------

func TestDisablingAnAccountEndsItsSessions(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	stud := e.student(admin, "target")
	var u models.User
	e.app.DB.First(&u, "normalized_user_name = ?", "TARGET")

	expect(t, stud.get("/api/dashboard"), 200)
	expect(t, admin.post("/api/admin/users/"+u.ID+"/disabled", map[string]any{"disabled": true}), 200)
	expect(t, stud.get("/api/dashboard"), 401)
	expect(t, e.client().login("target", "Student-passw0rd"), 401)

	expect(t, admin.post("/api/admin/users/"+u.ID+"/disabled", map[string]any{"disabled": false}), 200)
	expect(t, e.client().login("target", "Student-passw0rd"), 200)
}

func TestCannotDisableSelfOrAdmins(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	var me models.User
	e.app.DB.First(&me, "is_admin = ?", true)
	expect(t, admin.post("/api/admin/users/"+me.ID+"/disabled", map[string]any{"disabled": true}), 400)
	expect(t, admin.post("/api/admin/users/does-not-exist/disabled", map[string]any{"disabled": true}), 404)
}

func TestAdminCanInspectUsersReadOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	stud := e.student(admin, "inspected")
	sem := stud.newSemester(t, "Their semester")
	expect(t, stud.post("/api/semesters/"+sem+"/modules", moduleBody("Theirs")), 201)

	var u models.User
	e.app.DB.First(&u, "normalized_user_name = ?", "INSPECTED")
	detail := expect(t, admin.get("/api/admin/users/"+u.ID), 200).json()
	if len(detail["semesters"].([]any)) != 1 || num(detail["row"].(map[string]any)["modules"]) != 1 {
		t.Errorf("detail: %v", detail)
	}
	one := expect(t, admin.get("/api/admin/users/"+u.ID+"/semesters/"+sem), 200).json()
	if one["view"].(map[string]any)["semester"].(map[string]any)["name"] != "Their semester" {
		t.Errorf("semester view: %v", one)
	}
	expect(t, admin.get("/api/admin/users/"+u.ID+"/semesters/nope"), 404)
	expect(t, admin.get("/api/admin/users/nope"), 404)
}

func TestAdminUserSearchAndOverview(t *testing.T) {
	e := newEnv(t)
	admin := e.adminClient()
	e.student(admin, "alice")
	e.student(admin, "alina")
	e.student(admin, "bob_x")

	all := expect(t, admin.get("/api/admin/users"), 200).json()
	if num(all["total"]) != 4 {
		t.Errorf("total = %v", all["total"])
	}
	found := expect(t, admin.get("/api/admin/users?search=ali"), 200).json()
	if num(found["total"]) != 2 {
		t.Errorf("search 'ali' = %v", found["total"])
	}
	// LIKE wildcards in the search must be treated literally.
	if num(admin.get("/api/admin/users?search=%25").json()["total"]) != 0 {
		t.Error("'%' should not match everything")
	}
	if num(admin.get("/api/admin/users?search=_").json()["total"]) != 1 { // only bob_x has an underscore
		t.Error("'_' should match literally")
	}

	ov := expect(t, admin.get("/api/admin/overview?refresh=1"), 200).json()
	if num(ov["users"]) != 4 || num(ov["activeUsers"]) != 4 || ov["storage"] == nil {
		t.Errorf("overview: %v", ov)
	}
}

func TestSPAFallbackAndAPI404(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if r := c.get("/api/nope"); r.status != 404 || !strings.Contains(r.errText(), "Not found") {
		t.Errorf("api 404: %d %s", r.status, r.body)
	}
	if r := c.get("/assets/missing.js"); r.status != 404 {
		t.Errorf("missing asset = %d", r.status)
	}
}

func TestServesTheBuiltApp(t *testing.T) {
	if _, err := fs.Stat(web.FS(), "index.html"); err != nil {
		t.Skip("the frontend isn't built (run `npm run build` in frontend/)")
	}
	e := newEnv(t)
	c := e.client()
	for _, p := range []string{"/", "/semesters/abc", "/account/login"} { // client-side routes get the app shell
		r := expect(t, c.get(p), 200)
		if !strings.Contains(string(r.body), `id="app"`) || !strings.Contains(r.header.Get("Content-Type"), "text/html") {
			t.Errorf("%s did not return the app shell: %s", p, r.header.Get("Content-Type"))
		}
	}
}

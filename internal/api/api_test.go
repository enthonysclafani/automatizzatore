package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

const setupToken = "setup-token-for-tests"

type env struct {
	t     *testing.T
	api   *API
	srv   *httptest.Server
	store *store.Store
	now   time.Time
}

func manifestIn(ws, name string) string {
	return `apiVersion: pendolo/v1
kind: Automation
metadata: { name: ` + name + `, workspace: ` + ws + `, description: "` + name + ` checks" }
triggers: [ { schedule: { cron: "0 8 * * *", timezone: Europe/Rome } }, { manual: {} } ]
steps: [ { id: ping, type: http, request: { url: "https://example.com" } } ]
`
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	autos := filepath.Join(dir, "automations")
	for ws, name := range map[string]string{"ws-a": "alpha", "ws-b": "beta"} {
		_ = os.MkdirAll(filepath.Join(autos, name), 0o755)
		_ = os.WriteFile(filepath.Join(autos, name, "automation.yaml"), []byte(manifestIn(ws, name)), 0o644)
	}
	snap, err := registry.Load(autos)
	if err != nil || len(snap.Valid) != 2 {
		t.Fatalf("load: %v %+v", err, snap.Invalid)
	}
	ctx := context.Background()
	for name, a := range snap.Valid {
		if err := st.UpsertAutomation(ctx, store.Automation{Name: name, Workspace: a.Workspace(), Path: a.Dir, Hash: a.Hash, Valid: true}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	v, _ := vault.New(make([]byte, 32))
	cfg := config.Defaults()
	e := &env{t: t, store: st, now: time.Now()}
	e.api = New(&API{
		Store: st, Vault: v, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Snapshot: func() registry.Snapshot { return snap },
		Health:   func(context.Context) Health { return Health{OK: true, Version: "test", Runner: "local"} },
		Now:      func() time.Time { return e.now },
	})
	e.api.SetSetupToken(setupToken)
	e.srv = httptest.NewServer(e.api.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

// client is a browser with its own cookie jar.
type client struct {
	e      *env
	http   *http.Client
	bearer string
	csrf   bool
}

func (e *env) browser() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar}, csrf: true}
}

func (c *client) do(method, path string, body any, out any) int {
	c.e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.csrf {
		req.Header.Set("X-Pendolo-Request", "1")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.e.t.Fatalf("%s %s: %v\n%s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

type errBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func (c *client) expect(method, path string, body any, want int) {
	c.e.t.Helper()
	var eb errBody
	if got := c.do(method, path, body, &eb); got != want {
		c.e.t.Fatalf("%s %s: HTTP %d, want %d (%s %s)", method, path, got, want, eb.Code, eb.Error)
	}
}

// setupOwner creates the owner and returns a signed-in browser.
func (e *env) setupOwner() *client {
	c := e.browser()
	c.expect("POST", "/api/v1/setup", map[string]string{"token": setupToken, "email": "Owner@Example.com", "name": "Owner", "password": "owner-password-1"}, 200)
	return c
}

func (e *env) userWithRole(owner *client, email, ws, role string) *client {
	e.t.Helper()
	owner.expect("POST", "/api/v1/users", map[string]any{"email": email, "name": email, "password": "member-password-1"}, 201)
	if ws != "" {
		owner.expect("PUT", "/api/v1/workspaces/"+ws+"/members", map[string]string{"email": email, "role": role}, 204)
	}
	c := e.browser()
	c.expect("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "member-password-1"}, 200)
	return c
}

func TestSetup(t *testing.T) {
	e := newEnv(t)
	anon := e.browser()
	var st struct{ NeedsSetup bool }
	anon.do("GET", "/api/v1/setup", nil, &st)
	if !st.NeedsSetup {
		t.Fatal("needsSetup false with no users")
	}
	anon.csrf = false
	anon.expect("POST", "/api/v1/setup", map[string]string{"token": setupToken}, 403)
	anon.csrf = true
	anon.expect("POST", "/api/v1/setup", map[string]string{"token": "wrong", "email": "a@b.co", "password": "long-enough-pw"}, 401)

	owner := e.setupOwner()
	var me meResponse
	owner.do("GET", "/api/v1/auth/me", nil, &me)
	if !me.IsOwner || me.Email != "owner@example.com" || len(me.Workspaces) != 3 {
		t.Fatalf("me = %+v (want owner seeing default, ws-a, ws-b)", me)
	}
	e.browser().expect("POST", "/api/v1/setup", map[string]string{"token": setupToken, "email": "x@y.co", "password": "another-password"}, 409)
}

func TestSetupWhileOnlyMembersExist(t *testing.T) {
	e := newEnv(t)
	_ = e.store.CreateUser(context.Background(), &store.User{Email: "early@example.com", CreatedAt: e.now})
	var st struct{ NeedsSetup bool }
	e.browser().do("GET", "/api/v1/setup", nil, &st)
	if !st.NeedsSetup {
		t.Fatal("setup closed although no owner exists")
	}
	e.setupOwner()
}

func TestLoginAndTOTP(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	c := e.browser()
	c.expect("POST", "/api/v1/auth/login", map[string]string{"email": "owner@example.com", "password": "wrong-password"}, 401)
	c.expect("GET", "/api/v1/auth/me", nil, 401)

	var setup struct{ Secret, URI string }
	owner.do("POST", "/api/v1/auth/totp/setup", nil, &setup)
	code, _ := auth.TOTPCode(setup.Secret, e.now.Unix()/30, 6)
	owner.expect("POST", "/api/v1/auth/totp/enable", map[string]string{"code": code}, 204)

	e.now = e.now.Add(31 * time.Second)
	var eb errBody
	c.do("POST", "/api/v1/auth/login", map[string]string{"email": "owner@example.com", "password": "owner-password-1"}, &eb)
	if eb.Code != "totp_required" {
		t.Fatalf("login without code: %+v", eb)
	}
	code, _ = auth.TOTPCode(setup.Secret, e.now.Unix()/30, 6)
	c.expect("POST", "/api/v1/auth/login", map[string]string{"email": "owner@example.com", "password": "owner-password-1", "code": code}, 200)
	c2 := e.browser()
	c2.do("POST", "/api/v1/auth/login", map[string]string{"email": "owner@example.com", "password": "owner-password-1", "code": code}, &eb)
	if eb.Code != "totp_invalid" {
		t.Fatalf("replayed code: %+v", eb)
	}
	c.expect("POST", "/api/v1/auth/logout", nil, 204)
	c.expect("GET", "/api/v1/auth/me", nil, 401)
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	e.setupOwner()
	c := e.browser()
	var last int
	for i := 0; i < 10; i++ {
		last = c.do("POST", "/api/v1/auth/login", map[string]string{"email": "owner@example.com", "password": "nope-nope-nope"}, nil)
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after 10 failures: HTTP %d", last)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	owner.csrf = false
	owner.expect("POST", "/api/v1/workspaces/ws-a/automations/alpha/pause", nil, 403)
	owner.csrf = true
	owner.expect("POST", "/api/v1/workspaces/ws-a/automations/alpha/pause", nil, 204)
}

// The phase 2 exit criterion: a viewer sees only their own workspace.
func TestViewerSeesOnlyOwnWorkspace(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	ctx := context.Background()
	runB, _, _ := e.store.Enqueue(ctx, store.NewRun{Automation: "beta", Workspace: "ws-b", TriggerType: "manual"}, e.now)
	runA, _, _ := e.store.Enqueue(ctx, store.NewRun{Automation: "alpha", Workspace: "ws-a", TriggerType: "manual"}, e.now)
	owner.expect("PUT", "/api/v1/workspaces/ws-b/secrets/db-password", map[string]string{"value": "s3cret!"}, 204)

	viewer := e.userWithRole(owner, "viewer@example.com", "ws-a", "viewer")

	var me meResponse
	viewer.do("GET", "/api/v1/auth/me", nil, &me)
	if len(me.Workspaces) != 1 || me.Workspaces[0].ID != "ws-a" || me.Workspaces[0].Role != "viewer" {
		t.Fatalf("viewer workspaces = %+v", me.Workspaces)
	}

	// Their own workspace: readable.
	var list struct{ Automations []automationSummary }
	viewer.do("GET", "/api/v1/workspaces/ws-a/automations", nil, &list)
	if len(list.Automations) != 1 || list.Automations[0].Name != "alpha" {
		t.Fatalf("automations = %+v", list.Automations)
	}
	var runs struct{ Runs []store.Run }
	viewer.do("GET", "/api/v1/workspaces/ws-a/runs", nil, &runs)
	if len(runs.Runs) != 1 || runs.Runs[0].ID != runA {
		t.Fatalf("runs = %+v", runs.Runs)
	}
	viewer.expect("GET", "/api/v1/workspaces/ws-a/overview", nil, 200)
	viewer.expect("GET", "/api/v1/workspaces/ws-a/costs", nil, 200)
	viewer.expect("GET", "/api/v1/workspaces/ws-a/runs/"+runA, nil, 200)

	// Another workspace: as if it did not exist.
	for _, path := range []string{
		"/api/v1/workspaces/ws-b/overview", "/api/v1/workspaces/ws-b/automations", "/api/v1/workspaces/ws-b/automations/beta",
		"/api/v1/workspaces/ws-b/runs", "/api/v1/workspaces/ws-b/runs/" + runB, "/api/v1/workspaces/ws-b/costs",
		"/api/v1/workspaces/ws-b/secrets", "/api/v1/workspaces/nope/overview",
	} {
		viewer.expect("GET", path, nil, 404)
	}
	// A run of ws-b through the ws-a path, and an automation of ws-b through ws-a.
	viewer.expect("GET", "/api/v1/workspaces/ws-a/runs/"+runB, nil, 404)
	viewer.expect("GET", "/api/v1/workspaces/ws-a/automations/beta", nil, 404)

	// Read-only in their own workspace.
	viewer.expect("POST", "/api/v1/workspaces/ws-a/automations/alpha/trigger", nil, 403)
	viewer.expect("POST", "/api/v1/workspaces/ws-a/automations/alpha/pause", nil, 403)
	viewer.expect("GET", "/api/v1/workspaces/ws-a/secrets", nil, 403)
	viewer.expect("PUT", "/api/v1/workspaces/ws-a/secrets/x", map[string]string{"value": "v"}, 403)
	viewer.expect("GET", "/api/v1/workspaces/ws-a/members", nil, 403)
	viewer.expect("GET", "/api/v1/workspaces/ws-a/audit", nil, 403)
	// And no owner endpoints.
	for _, path := range []string{"/api/v1/users", "/api/v1/system", "/api/v1/audit"} {
		viewer.expect("GET", path, nil, 403)
	}
}

func TestRolesInAWorkspace(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	builder := e.userWithRole(owner, "builder@example.com", "ws-a", "builder")
	admin := e.userWithRole(owner, "admin@example.com", "ws-a", "admin")

	var trig struct{ RunID string }
	if code := builder.do("POST", "/api/v1/workspaces/ws-a/automations/alpha/trigger", nil, &trig); code != 202 || trig.RunID == "" {
		t.Fatalf("builder trigger: %d %+v", code, trig)
	}
	builder.expect("GET", "/api/v1/workspaces/ws-a/secrets", nil, 200)
	builder.expect("PUT", "/api/v1/workspaces/ws-a/secrets/api-key", map[string]string{"value": "v"}, 403)
	builder.expect("GET", "/api/v1/workspaces/ws-a/members", nil, 403)

	admin.expect("PUT", "/api/v1/workspaces/ws-a/secrets/api-key", map[string]string{"value": "value-1"}, 204)
	admin.expect("PUT", "/api/v1/workspaces/ws-a/secrets/bad name", map[string]string{"value": "v"}, 400)
	var secretsList struct{ Secrets []store.SecretInfo }
	builder.do("GET", "/api/v1/workspaces/ws-a/secrets", nil, &secretsList)
	if len(secretsList.Secrets) != 1 || secretsList.Secrets[0].UpdatedBy != "admin@example.com" {
		t.Fatalf("secrets = %+v", secretsList.Secrets)
	}
	var members struct{ Members []member }
	admin.do("GET", "/api/v1/workspaces/ws-a/members", nil, &members)
	if len(members.Members) != 2 {
		t.Fatalf("members = %+v", members.Members)
	}
	// An admin of ws-a is nobody in ws-b.
	admin.expect("PUT", "/api/v1/workspaces/ws-b/members", map[string]string{"email": "admin@example.com", "role": "admin"}, 404)

	var audit struct{ Entries []store.AuditEntry }
	admin.do("GET", "/api/v1/workspaces/ws-a/audit", nil, &audit)
	actions := map[string]bool{}
	for _, en := range audit.Entries {
		actions[en.Action] = true
		if en.Workspace != "ws-a" {
			t.Fatalf("audit of ws-a shows %+v", en)
		}
	}
	for _, want := range []string{"automation.trigger", "secret.update", "member.set"} {
		if !actions[want] {
			t.Errorf("audit lacks %s: %+v", want, actions)
		}
	}
}

func TestAPITokens(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	builder := e.userWithRole(owner, "bot@example.com", "ws-a", "builder")
	var tok struct {
		ID    string
		Token string
	}
	if code := builder.do("POST", "/api/v1/tokens", map[string]any{"name": "ci", "expiresInDays": 30}, &tok); code != 201 || !strings.HasPrefix(tok.Token, "pdl_") {
		t.Fatalf("create token: %d %+v", code, tok)
	}
	script := &client{e: e, http: http.DefaultClient, bearer: tok.Token} // no cookies, no CSRF header
	script.expect("POST", "/api/v1/workspaces/ws-a/automations/alpha/trigger", nil, 202)
	script.expect("GET", "/api/v1/workspaces/ws-b/runs", nil, 404)
	builder.expect("DELETE", "/api/v1/tokens/"+tok.ID, nil, 204)
	script.expect("GET", "/api/v1/workspaces/ws-a/runs", nil, 401)
}

func TestUsersAdministration(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	var me meResponse
	owner.do("GET", "/api/v1/auth/me", nil, &me)
	owner.expect("PATCH", "/api/v1/users/"+me.ID, map[string]any{"isOwner": false}, 409)
	owner.expect("POST", "/api/v1/users", map[string]any{"email": "weak@example.com", "password": "short"}, 400)
	owner.expect("POST", "/api/v1/users", map[string]any{"email": "owner@example.com", "password": "long-enough-123"}, 409)
	owner.expect("POST", "/api/v1/workspaces", map[string]string{"id": "Bad ID"}, 400)
	owner.expect("POST", "/api/v1/workspaces", map[string]string{"id": "client-x", "name": "Client X"}, 201)

	member := e.userWithRole(owner, "m@example.com", "client-x", "viewer")
	var users struct{ Users []store.User }
	owner.do("GET", "/api/v1/users", nil, &users)
	var id string
	for _, u := range users.Users {
		if u.Email == "m@example.com" {
			id = u.ID
		}
	}
	owner.expect("PATCH", "/api/v1/users/"+id, map[string]any{"disabled": true}, 200)
	member.expect("GET", "/api/v1/auth/me", nil, 401)
	var sys struct{ Version string }
	owner.do("GET", "/api/v1/system", nil, &sys)
	if sys.Version != "test" {
		t.Fatalf("system = %+v", sys)
	}
}

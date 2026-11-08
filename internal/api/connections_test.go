package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/testfakes"
)

func TestConnectionsAndOAuth(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	g := testfakes.NewGoogle(t)
	var gotVerifier string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotVerifier = r.Form.Get("code_verifier")
		if r.Form.Get("code") != "the-code" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "rt", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer tokenSrv.Close()
	e.api.Hub.Endpoints = map[string]string{"gmail": g.Server.URL, "google_token": tokenSrv.URL, "google_auth": "https://accounts.example/auth"}

	var cat struct {
		Kinds            []struct{ Kind string }
		OAuthRedirectURL string
	}
	owner.do("GET", "/api/v1/connectors", nil, &cat)
	if len(cat.Kinds) != 4 || cat.OAuthRedirectURL != "http://localhost:8080/api/v1/oauth/callback" {
		t.Fatalf("catalog = %+v", cat)
	}

	owner.expect("PUT", "/api/v1/workspaces/ws-a/connections/google", map[string]any{"kind": "google", "config": map[string]any{"clientId": "cid"}}, 400)
	var c connectionView
	owner.do("PUT", "/api/v1/workspaces/ws-a/connections/google", map[string]any{
		"kind": "google", "config": map[string]any{"clientId": "cid"}, "secrets": map[string]string{"clientSecret": "csecret"},
	}, &c)
	if c.Status != store.ConnNeedsAuth || strings.Join(c.SecretsSet, ",") != "clientSecret" || len(c.Actions) != 7 {
		t.Fatalf("google connection = %+v", c)
	}

	var start struct{ URL string }
	owner.do("POST", "/api/v1/workspaces/ws-a/connections/google/oauth/start", nil, &start)
	auth, _ := url.Parse(start.URL)
	state := auth.Query().Get("state")
	if auth.Host != "accounts.example" || auth.Query().Get("access_type") != "offline" || auth.Query().Get("code_challenge") == "" || state == "" {
		t.Fatalf("auth URL = %s", start.URL)
	}

	// Another browser cannot finish the sign-in (a consent link sent to someone else).
	stranger := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var start2 struct{ URL string }
	owner.do("POST", "/api/v1/workspaces/ws-a/connections/google/oauth/start", nil, &start2)
	auth2, _ := url.Parse(start2.URL)
	resp, err := stranger.Get(e.srv.URL + "/api/v1/oauth/callback?state=" + auth2.Query().Get("state") + "&code=the-code")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "error=Finish") || gotVerifier != "" {
		t.Fatalf("stranger callback = %q", loc)
	}

	// The browser that started it can.
	noFollow := &http.Client{Jar: owner.http.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noFollow.Get(e.srv.URL + "/api/v1/oauth/callback?state=" + state + "&code=the-code")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/w/ws-a/connections?connected=google" || gotVerifier == "" {
		t.Fatalf("callback redirect %q, verifier %q", loc, gotVerifier)
	}
	stored, _ := e.store.GetConnection(context.Background(), "ws-a", "google")
	if stored.Status != store.ConnReady || stored.Account != "me@example.com" {
		t.Fatalf("after sign-in: %+v", stored)
	}
	// A state is single use.
	resp, _ = noFollow.Get(e.srv.URL + "/api/v1/oauth/callback?state=" + state + "&code=the-code")
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Location"), "error=") {
		t.Fatalf("replayed state accepted: %s", resp.Header.Get("Location"))
	}

	var test struct {
		OK     bool
		Detail string
	}
	owner.do("POST", "/api/v1/workspaces/ws-a/connections/google/test", nil, &test)
	if !test.OK || !strings.Contains(test.Detail, "me@example.com") {
		t.Fatalf("test = %+v", test)
	}

	// MCP: headers are secrets, tools are discovered.
	seo := testfakes.MCP(t, "k1")
	owner.do("PUT", "/api/v1/workspaces/ws-a/connections/openseo", map[string]any{
		"kind": "mcp", "config": map[string]any{"url": seo.URL}, "secrets": map[string]string{"Authorization": "Bearer k1"},
	}, &c)
	if c.Status != store.ConnReady || strings.Join(c.SecretsSet, ",") != "Authorization" || len(c.Actions) != 3 {
		t.Fatalf("mcp connection = %+v", c)
	}
	risks := map[string]string{}
	for _, a := range c.Actions {
		risks[a.Name] = string(a.Risk)
	}
	if risks["openseo.research_keywords"] != "read" || risks["openseo.delete_project"] != "destructive" {
		t.Fatalf("risks = %+v", risks)
	}
	var list struct{ Connections []connectionView }
	owner.do("GET", "/api/v1/workspaces/ws-a/connections", nil, &list)
	raw, _ := json.Marshal(list)
	if len(list.Connections) != 2 || strings.Contains(string(raw), "csecret") || strings.Contains(string(raw), "k1") || strings.Contains(string(raw), `"rt"`) {
		t.Fatalf("list leaks secrets or is wrong: %s", raw)
	}
	owner.expect("DELETE", "/api/v1/workspaces/ws-a/connections/openseo", nil, 204)
}

func TestApprovalsAPI(t *testing.T) {
	e := newEnv(t)
	owner := e.setupOwner()
	builder := e.userWithRole(owner, "builder@example.com", "ws-a", "builder")
	approver := e.userWithRole(owner, "approver@example.com", "ws-a", "approver")
	ctx := context.Background()

	pause := func(approvers []string) string {
		run, _ := e.store.StartNow(ctx, store.NewRun{Automation: "alpha", Workspace: "ws-a", TriggerType: "manual"}, "w", e.now)
		id := store.NewID(e.now)
		if err := e.store.Pause(ctx, store.Approval{ID: id, RunID: run.ID, Workspace: "ws-a", Automation: "alpha", StepPath: "send",
			Message: "send it?", Approvers: approvers, OnTimeout: "reject", RequestedAt: e.now, ExpiresAt: e.now.Add(time.Hour)}, `{"next":1}`, 0, 0); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := pause([]string{"approver", "admin"})
	var list struct{ Approvals []store.Approval }
	builder.do("GET", "/api/v1/workspaces/ws-a/approvals?status=pending", nil, &list)
	if len(list.Approvals) != 1 {
		t.Fatalf("pending = %+v", list.Approvals)
	}
	builder.expect("POST", "/api/v1/workspaces/ws-a/approvals/"+id+"/approve", nil, 403)
	approver.expect("POST", "/api/v1/workspaces/ws-a/approvals/"+id+"/approve", map[string]string{"comment": "go"}, 200)
	approver.expect("POST", "/api/v1/workspaces/ws-a/approvals/"+id+"/reject", nil, 409)
	var ov struct{ WaitingApprovals int }
	owner.do("GET", "/api/v1/workspaces/ws-a/overview", nil, &ov)
	if ov.WaitingApprovals != 0 {
		t.Fatalf("waiting = %d", ov.WaitingApprovals)
	}

	adminOnly := pause([]string{"admin"})
	approver.expect("POST", "/api/v1/workspaces/ws-a/approvals/"+adminOnly+"/approve", nil, 403)
	owner.expect("POST", "/api/v1/workspaces/ws-a/approvals/"+adminOnly+"/reject", map[string]string{"comment": "no"}, 200)
	owner.expect("POST", "/api/v1/workspaces/ws-b/approvals/"+adminOnly+"/approve", nil, 404)

	owner.expect("PATCH", "/api/v1/workspaces/ws-a/settings", map[string]string{"maxAutonomy": "always"}, 400)
	var s store.WorkspaceSettings
	owner.do("PATCH", "/api/v1/workspaces/ws-a/settings", map[string]string{"maxAutonomy": "draft", "name": "Client A"}, &s)
	if s.MaxAutonomy != "draft" || s.Name != "Client A" {
		t.Fatalf("settings = %+v", s)
	}
}

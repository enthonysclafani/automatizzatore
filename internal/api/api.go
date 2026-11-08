// Package api is the JSON API behind the dashboard, under /api/v1.
//
// Browsers sign in with a session cookie; scripts use API tokens. Every
// workspace endpoint checks the caller's role in that workspace, and a
// workspace the caller does not belong to answers 404, as if it did not
// exist. See docs/api.md.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/config"
	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/engine"
	"github.com/enthonysclafani/automatizzatore/internal/registry"
	"github.com/enthonysclafani/automatizzatore/internal/store"
	"github.com/enthonysclafani/automatizzatore/internal/vault"
)

// Health is the state of the server, shown by /healthz and the dashboard.
type Health struct {
	OK          bool      `json:"ok"`
	Reason      string    `json:"reason,omitempty"`
	Version     string    `json:"version"`
	StartedAt   time.Time `json:"startedAt"`
	LastTick    time.Time `json:"lastTick"`
	Leader      bool      `json:"leader"`
	Automations int       `json:"automations"`
	Invalid     int       `json:"invalid"`
	Running     int32     `json:"running"`
	Queued      int       `json:"queued"`
	Runner      string    `json:"runner"`
}

// API serves /api/v1.
type API struct {
	Store  *store.Store
	Vault  *vault.Vault
	Hub    *connectors.Hub
	Config config.Config
	Log    *slog.Logger
	// Snapshot returns the automations currently loaded.
	Snapshot func() registry.Snapshot
	// Health returns the server's state.
	Health func(ctx context.Context) Health
	// Wake tells the workers that a run was queued.
	Wake func()
	Now  func() time.Time

	loginByIP    auth.Limiter
	loginByEmail auth.Limiter

	setupMu    sync.Mutex
	setupToken string

	oidcMu   sync.Mutex
	oidcProv *oidcProvider

	oauthMu     sync.Mutex
	oauthStates map[string]oauthState
}

// New returns an API with its rate limits set.
func New(a *API) *API {
	a.loginByIP = auth.Limiter{Max: 30, Window: 15 * time.Minute}
	a.loginByEmail = auth.Limiter{Max: 8, Window: 15 * time.Minute}
	if a.Now == nil {
		a.Now = time.Now
	}
	if a.Log == nil {
		a.Log = slog.Default()
	}
	if a.Wake == nil {
		a.Wake = func() {}
	}
	return a
}

// SetSetupToken arms the first-run setup with a one-time token.
func (a *API) SetSetupToken(token string) {
	a.setupMu.Lock()
	a.setupToken = token
	a.setupMu.Unlock()
}

func (a *API) checkSetupToken(token string) bool {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	return a.setupToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.setupToken)) == 1
}

// Permissions in a workspace.
const (
	permView         = "view"
	permOperate      = "operate"       // trigger, pause, resume
	permSecretsRead  = "secrets.read"  // list secret names
	permSecretsWrite = "secrets.write" // create, change, delete secrets
	permManage       = "manage"        // members, audit log, connections, settings
	permConnTest     = "connections.test"
	permApprove      = "approve" // further limited by the approval's own approvers
)

// RoleOwner is the effective role of instance owners in every workspace.
const RoleOwner = "owner"

func can(role, perm string) bool {
	switch role {
	case RoleOwner, store.RoleAdmin:
		return true
	case store.RoleBuilder:
		return perm == permView || perm == permOperate || perm == permSecretsRead || perm == permConnTest
	case store.RoleApprover:
		return perm == permView || perm == permApprove
	case store.RoleViewer:
		return perm == permView
	}
	return false
}

// Handler returns the routes under /api/v1.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	public := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, h) }
	signedIn := func(pattern string, h func(http.ResponseWriter, *http.Request, *principal)) {
		mux.Handle(pattern, a.requireUser(h, false))
	}
	owner := func(pattern string, h func(http.ResponseWriter, *http.Request, *principal)) {
		mux.Handle(pattern, a.requireUser(h, true))
	}
	ws := func(pattern, perm string, h func(http.ResponseWriter, *http.Request, *principal, string)) {
		mux.Handle(pattern, a.requireWorkspace(perm, h))
	}

	public("GET /api/v1/setup", a.getSetup)
	public("POST /api/v1/setup", a.postSetup)
	public("POST /api/v1/auth/login", a.login)
	public("GET /api/v1/auth/oidc/login", a.oidcLogin)
	public("GET /api/v1/auth/oidc/callback", a.oidcCallback)
	public("GET /api/v1/oauth/callback", a.oauthCallback)
	signedIn("GET /api/v1/connectors", a.catalog)

	signedIn("POST /api/v1/auth/logout", a.logout)
	signedIn("GET /api/v1/auth/me", a.me)
	signedIn("POST /api/v1/auth/password", a.changePassword)
	signedIn("POST /api/v1/auth/totp/setup", a.totpSetup)
	signedIn("POST /api/v1/auth/totp/enable", a.totpEnable)
	signedIn("POST /api/v1/auth/totp/disable", a.totpDisable)
	signedIn("GET /api/v1/tokens", a.listTokens)
	signedIn("POST /api/v1/tokens", a.createToken)
	signedIn("DELETE /api/v1/tokens/{id}", a.deleteToken)

	ws("GET /api/v1/workspaces/{ws}/overview", permView, a.overview)
	ws("GET /api/v1/workspaces/{ws}/automations", permView, a.listAutomations)
	ws("GET /api/v1/workspaces/{ws}/automations/{name}", permView, a.getAutomation)
	ws("POST /api/v1/workspaces/{ws}/automations/{name}/trigger", permOperate, a.triggerAutomation)
	ws("POST /api/v1/workspaces/{ws}/automations/{name}/pause", permOperate, a.pauseAutomation)
	ws("POST /api/v1/workspaces/{ws}/automations/{name}/resume", permOperate, a.resumeAutomation)
	ws("GET /api/v1/workspaces/{ws}/runs", permView, a.listRuns)
	ws("GET /api/v1/workspaces/{ws}/runs/{id}", permView, a.getRun)
	ws("GET /api/v1/workspaces/{ws}/costs", permView, a.costs)
	ws("GET /api/v1/workspaces/{ws}/secrets", permSecretsRead, a.listSecrets)
	ws("PUT /api/v1/workspaces/{ws}/secrets/{name}", permSecretsWrite, a.putSecret)
	ws("DELETE /api/v1/workspaces/{ws}/secrets/{name}", permSecretsWrite, a.deleteSecret)
	ws("GET /api/v1/workspaces/{ws}/members", permManage, a.listMembers)
	ws("PUT /api/v1/workspaces/{ws}/members", permManage, a.putMember)
	ws("DELETE /api/v1/workspaces/{ws}/members/{userId}", permManage, a.deleteMember)
	ws("GET /api/v1/workspaces/{ws}/audit", permManage, a.workspaceAudit)
	ws("GET /api/v1/workspaces/{ws}/connections", permView, a.listConnections)
	ws("PUT /api/v1/workspaces/{ws}/connections/{name}", permManage, a.putConnection)
	ws("DELETE /api/v1/workspaces/{ws}/connections/{name}", permManage, a.deleteConnection)
	ws("POST /api/v1/workspaces/{ws}/connections/{name}/test", permConnTest, a.testConnection)
	ws("POST /api/v1/workspaces/{ws}/connections/{name}/oauth/start", permManage, a.oauthStart)
	ws("GET /api/v1/workspaces/{ws}/approvals", permView, a.listApprovals)
	ws("POST /api/v1/workspaces/{ws}/approvals/{id}/approve", permApprove, a.approve)
	ws("POST /api/v1/workspaces/{ws}/approvals/{id}/reject", permApprove, a.reject)
	ws("GET /api/v1/workspaces/{ws}/settings", permView, a.getSettings)
	ws("PATCH /api/v1/workspaces/{ws}/settings", permManage, a.patchSettings)

	owner("POST /api/v1/workspaces", a.createWorkspace)
	owner("GET /api/v1/users", a.listUsers)
	owner("POST /api/v1/users", a.createUser)
	owner("PATCH /api/v1/users/{id}", a.updateUser)
	owner("GET /api/v1/audit", a.globalAudit)
	owner("GET /api/v1/system", a.system)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return a.csrf(mux)
}

// csrf rejects state-changing requests without the custom header, unless
// they carry a bearer token (which browsers never add on their own).
func (a *API) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && r.Header.Get("X-Pendolo-Request") != "1" {
				writeError(w, http.StatusForbidden, "csrf", "missing X-Pendolo-Request header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// apiError is the JSON error body.
type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

func (a *API) internalError(w http.ResponseWriter, err error) {
	a.Log.Error("api", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error; see the server log")
}

func notFound(w http.ResponseWriter, what string) {
	writeError(w, http.StatusNotFound, "not_found", what+" not found")
}

// decode reads a JSON body of at most 1 MiB into v, rejecting unknown fields.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body: "+err.Error())
		return false
	}
	return true
}

// clientIP is the caller's address, from X-Forwarded-For when trusted.
func (a *API) clientIP(r *http.Request) string {
	if a.Config.Server.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			first, _, _ := strings.Cut(fwd, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *API) secureRequest(r *http.Request) bool {
	return r.TLS != nil || (a.Config.Server.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

func (a *API) audit(r *http.Request, p *principal, action, workspace, target string, detail map[string]any) {
	actor := "anonymous"
	if p != nil {
		actor = p.label()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Store.Audit(ctx, store.AuditEntry{
		At: a.Now(), Actor: actor, Action: action, Workspace: workspace, Target: target, Detail: detail, IP: a.clientIP(r),
	}); err != nil {
		a.Log.Error("audit", "action", action, "err", err)
	}
}

// automationIn returns a loaded automation if it belongs to the workspace.
func (a *API) automationIn(ws, name string) (engine.Automation, *registry.Invalid, bool) {
	snap := a.Snapshot()
	if au, ok := snap.Valid[name]; ok && au.Workspace() == ws {
		return au, nil, true
	}
	for i := range snap.Invalid {
		if snap.Invalid[i].Name == name && snap.Invalid[i].Workspace == ws {
			return engine.Automation{}, &snap.Invalid[i], true
		}
	}
	return engine.Automation{}, nil, false
}

func badRequest(w http.ResponseWriter, format string, args ...any) {
	writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf(format, args...))
}

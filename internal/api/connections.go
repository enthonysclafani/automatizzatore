package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/connectors"
	"github.com/enthonysclafani/automatizzatore/internal/manifest"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

var connNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type connectionView struct {
	Name       string                  `json:"name"`
	Kind       string                  `json:"kind"`
	Config     map[string]any          `json:"config"`
	SecretsSet []string                `json:"secretsSet"`
	Status     string                  `json:"status"`
	Error      string                  `json:"error,omitempty"`
	Account    string                  `json:"account,omitempty"`
	UpdatedAt  time.Time               `json:"updatedAt"`
	UpdatedBy  string                  `json:"updatedBy"`
	Actions    []connectors.ActionInfo `json:"actions"`
}

func (a *API) viewConnection(r *http.Request, c *store.Connection) connectionView {
	v := connectionView{
		Name: c.Name, Kind: c.Kind, Config: c.Config, SecretsSet: []string{}, Status: c.Status, Error: c.Error,
		Account: c.Account, UpdatedAt: c.UpdatedAt, UpdatedBy: c.UpdatedBy, Actions: []connectors.ActionInfo{},
	}
	if conn, err := a.Hub.Open(c); err == nil {
		v.SecretsSet = connectors.ConnectionSecretNames(conn)
	}
	if acts := a.Hub.Actions(r.Context(), c); acts != nil {
		v.Actions = acts
	}
	return v
}

func (a *API) catalog(w http.ResponseWriter, _ *http.Request, _ *principal) {
	redirect := strings.TrimRight(a.Config.Server.PublicURL, "/") + connectors.OAuthCallbackPath
	writeJSON(w, http.StatusOK, map[string]any{"kinds": connectors.Catalog(redirect), "oauthRedirectURL": redirect})
}

func (a *API) listConnections(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	list, err := a.Store.ListConnections(r.Context(), ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	out := make([]connectionView, 0, len(list))
	for i := range list {
		out = append(out, a.viewConnection(r, &list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func kindInfo(kind string) (connectors.KindInfo, bool) {
	for _, k := range connectors.Catalog("") {
		if k.Kind == kind {
			return k, true
		}
	}
	return connectors.KindInfo{}, false
}

func (a *API) putConnection(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	name := r.PathValue("name")
	if !connNameRe.MatchString(name) {
		badRequest(w, "connection names use lowercase letters, digits, '_' and '-', starting with a letter")
		return
	}
	var req struct {
		Kind    string            `json:"kind"`
		Config  map[string]any    `json:"config"`
		Secrets map[string]string `json:"secrets"`
	}
	if !decode(w, r, &req) {
		return
	}
	info, ok := kindInfo(req.Kind)
	if !ok {
		badRequest(w, "unknown kind %q: use google, slack, websearch or mcp", req.Kind)
		return
	}
	existing, err := a.Store.GetConnection(r.Context(), ws, name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.internalError(w, err)
		return
	}
	secretsNow := map[string]string{}
	if existing != nil {
		if existing.Kind != req.Kind {
			writeError(w, http.StatusConflict, "conflict", "a "+existing.Kind+" connection already uses this name")
			return
		}
		conn, err := a.Hub.Open(existing)
		if err != nil {
			a.internalError(w, err)
			return
		}
		secretsNow = conn.Secrets
	}

	config := map[string]any{}
	secretField := map[string]bool{}
	for _, f := range info.Fields {
		secretField[f.Name] = f.Secret
		if f.Secret {
			continue
		}
		if v, ok := req.Config[f.Name]; ok && v != nil && v != "" {
			config[f.Name] = v
		}
	}
	for key, value := range req.Secrets {
		switch {
		case req.Kind == connectors.KindMCP && key == "headers":
			var headers map[string]string
			if err := json.Unmarshal([]byte(value), &headers); err != nil {
				badRequest(w, "headers must be a JSON object of header names to values")
				return
			}
			for k, v := range headers {
				secretsNow[connectors.HeaderPrefix+k] = v
			}
		case req.Kind == connectors.KindMCP:
			secretsNow[connectors.HeaderPrefix+key] = value
		case secretField[key] && value == "":
			delete(secretsNow, key)
		case secretField[key]:
			secretsNow[key] = value
		default:
			badRequest(w, "%s is not a secret field of %s connections", key, req.Kind)
			return
		}
	}
	if req.Kind == connectors.KindMCP {
		if hdrs, ok := req.Config["headers"].(map[string]any); ok {
			for k, v := range hdrs {
				if s, ok := v.(string); ok {
					secretsNow[connectors.HeaderPrefix+k] = s
				}
			}
		}
		for k, v := range secretsNow {
			if v == "" {
				delete(secretsNow, k)
			}
		}
	}
	for _, f := range info.Fields {
		if !f.Required {
			continue
		}
		have := config[f.Name] != nil
		if f.Secret {
			have = secretsNow[f.Name] != ""
		}
		if !have {
			badRequest(w, "%s is required", f.Label)
			return
		}
	}
	if req.Kind == connectors.KindMCP {
		if u, err := url.Parse(str(config, "url")); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			badRequest(w, "the server URL must start with https:// or http://")
			return
		}
	}

	status := store.ConnReady
	if req.Kind == connectors.KindGoogle {
		if existing != nil && str(existing.Config, "clientId") != str(config, "clientId") {
			delete(secretsNow, "token")
		}
		if secretsNow["token"] == "" {
			status = store.ConnNeedsAuth
		}
	}
	sealed, err := a.Hub.Seal(ws, name, secretsNow)
	if err != nil {
		a.internalError(w, err)
		return
	}
	c := store.Connection{
		Workspace: ws, Name: name, Kind: req.Kind, Config: config, Sealed: sealed, Status: status,
		UpdatedAt: a.Now(), UpdatedBy: p.User.Email,
	}
	if existing != nil && status == store.ConnReady {
		c.Account = existing.Account
	}
	if err := a.Store.PutConnection(r.Context(), c); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "connection.update", ws, name, map[string]any{"kind": req.Kind})
	writeJSON(w, http.StatusOK, a.viewConnection(r, &c))
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func (a *API) deleteConnection(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	name := r.PathValue("name")
	err := a.Store.DeleteConnection(r.Context(), ws, name)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "connection")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "connection.delete", ws, name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testConnection(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	c, err := a.Store.GetConnection(r.Context(), ws, r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "connection")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	ok, detail := a.Hub.Test(r.Context(), c)
	a.audit(r, p, "connection.test", ws, c.Name, map[string]any{"ok": ok})
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "detail": detail})
}

// oauthState is a pending OAuth sign-in.
type oauthState struct {
	workspace, connection, verifier, user string
	expires                               time.Time
}

func (a *API) oauthStart(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	c, err := a.Store.GetConnection(r.Context(), ws, r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "connection")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	state, verifier := auth.Token(""), oauth2.GenerateVerifier()
	target, err := a.Hub.OAuthStart(r.Context(), c, state, verifier)
	if err != nil {
		badRequest(w, "%v", err)
		return
	}
	a.oauthMu.Lock()
	if a.oauthStates == nil {
		a.oauthStates = map[string]oauthState{}
	}
	now := a.Now()
	for k, s := range a.oauthStates {
		if now.After(s.expires) {
			delete(a.oauthStates, k)
		}
	}
	a.oauthStates[state] = oauthState{workspace: ws, connection: c.Name, verifier: verifier, user: p.User.Email, expires: now.Add(15 * time.Minute)}
	a.oauthMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"url": target})
}

func (a *API) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.oauthMu.Lock()
	st, ok := a.oauthStates[q.Get("state")]
	delete(a.oauthStates, q.Get("state"))
	a.oauthMu.Unlock()
	if !ok || a.Now().After(st.expires) {
		http.Redirect(w, r, "/?error="+url.QueryEscape("The sign-in expired. Start it again from the Connections page."), http.StatusFound)
		return
	}
	back := "/w/" + url.PathEscape(st.workspace) + "/connections"
	fail := func(msg string) {
		http.Redirect(w, r, back+"?error="+url.QueryEscape(msg), http.StatusFound)
	}
	if e := q.Get("error"); e != "" {
		fail("Google refused the sign-in: " + e)
		return
	}
	c, err := a.Store.GetConnection(r.Context(), st.workspace, st.connection)
	if err != nil {
		fail("The connection no longer exists.")
		return
	}
	if err := a.Hub.OAuthFinish(r.Context(), c, q.Get("code"), st.verifier); err != nil {
		fail(err.Error())
		return
	}
	actor := &principal{User: &store.User{Email: st.user}}
	a.audit(r, actor, "connection.oauth", st.workspace, st.connection, nil)
	http.Redirect(w, r, back+"?connected="+url.QueryEscape(st.connection), http.StatusFound)
}

func (a *API) listApprovals(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	list, err := a.Store.ListApprovals(r.Context(), store.ApprovalFilter{Workspace: ws, Status: r.URL.Query().Get("status")})
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": list})
}

func (a *API) approve(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	a.decide(w, r, p, ws, store.ApprovalApproved)
}

func (a *API) reject(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	a.decide(w, r, p, ws, store.ApprovalRejected)
}

func (a *API) decide(w http.ResponseWriter, r *http.Request, p *principal, ws, status string) {
	var req struct {
		Comment string `json:"comment"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	ap, err := a.Store.GetApproval(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && ap.Workspace != ws) {
		notFound(w, "approval")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	role, err := a.roleIn(r.Context(), p, ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	allowed := role == RoleOwner || role == store.RoleAdmin
	for _, r := range ap.Approvers {
		allowed = allowed || r == role
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "only "+strings.Join(ap.Approvers, ", ")+" can decide this approval")
		return
	}
	if len(req.Comment) > 2000 {
		badRequest(w, "keep the comment under 2000 characters")
		return
	}
	decided, err := a.Store.Decide(r.Context(), ap.ID, status, p.User.Email, strings.TrimSpace(req.Comment), a.Now())
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "this approval was already "+decided.Status)
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	if status == store.ApprovalApproved {
		a.Wake()
	}
	a.audit(r, p, "approval."+status, ws, ap.Automation, map[string]any{"run": ap.RunID, "step": ap.StepPath})
	writeJSON(w, http.StatusOK, decided)
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request, _ *principal, ws string) {
	s, err := a.Store.GetWorkspaceSettings(r.Context(), ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) patchSettings(w http.ResponseWriter, r *http.Request, p *principal, ws string) {
	var req struct {
		Name        *string `json:"name"`
		MaxAutonomy *string `json:"maxAutonomy"`
	}
	if !decode(w, r, &req) {
		return
	}
	s, err := a.Store.GetWorkspaceSettings(r.Context(), ws)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if req.Name != nil {
		if n := strings.TrimSpace(*req.Name); n != "" {
			s.Name = n
		}
	}
	if req.MaxAutonomy != nil {
		switch *req.MaxAutonomy {
		case manifest.AutonomyObserve, manifest.AutonomyDraft, manifest.AutonomyAsk, manifest.AutonomyAuto:
			s.MaxAutonomy = *req.MaxAutonomy
		default:
			badRequest(w, "maxAutonomy must be observe, draft, ask or auto")
			return
		}
	}
	if err := a.Store.SetWorkspaceSettings(r.Context(), *s); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "workspace.settings", ws, ws, map[string]any{"name": s.Name, "maxAutonomy": s.MaxAutonomy})
	writeJSON(w, http.StatusOK, s)
}

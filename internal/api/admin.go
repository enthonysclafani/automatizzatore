package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

var workspaceIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (a *API) createWorkspace(w http.ResponseWriter, r *http.Request, p *principal) {
	var req struct{ ID, Name string }
	if !decode(w, r, &req) {
		return
	}
	if !workspaceIDRe.MatchString(req.ID) {
		badRequest(w, "the id uses lowercase letters, digits and hyphens, as in metadata.workspace")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.ID
	}
	err := a.Store.CreateWorkspace(r.Context(), store.Workspace{ID: req.ID, Name: name, CreatedAt: a.Now()})
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "a workspace with this id exists")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "workspace.create", req.ID, req.ID, map[string]any{"name": name})
	writeJSON(w, http.StatusCreated, workspaceRef{ID: req.ID, Name: name, Role: RoleOwner})
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request, _ *principal) {
	users, err := a.Store.ListUsers(r.Context())
	if err != nil {
		a.internalError(w, err)
		return
	}
	if users == nil {
		users = []store.User{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request, p *principal) {
	var req struct {
		Email, Name, Password string
		IsOwner               bool
	}
	if !decode(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(req.Email) {
		badRequest(w, "invalid email address")
		return
	}
	u := &store.User{Email: req.Email, Name: strings.TrimSpace(req.Name), IsOwner: req.IsOwner, CreatedAt: a.Now()}
	if req.Password != "" {
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			badRequest(w, "%v", err)
			return
		}
		u.PasswordHash = hash
	} else if !a.Config.Auth.OIDC.Enabled() {
		badRequest(w, "set an initial password (single sign-on is not configured)")
		return
	}
	err := a.Store.CreateUser(r.Context(), u)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "a user with this email exists")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "user.create", "", u.Email, map[string]any{"owner": u.IsOwner})
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request, p *principal) {
	var req struct {
		Name     *string
		Disabled *bool
		IsOwner  *bool
		Password *string
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := a.Store.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "user")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	losesOwner := u.IsOwner && !u.Disabled && ((req.IsOwner != nil && !*req.IsOwner) || (req.Disabled != nil && *req.Disabled))
	if losesOwner {
		if n, err := a.Store.CountOwners(r.Context()); err != nil {
			a.internalError(w, err)
			return
		} else if n <= 1 {
			writeError(w, http.StatusConflict, "last_owner", "this is the last owner; make someone else owner first")
			return
		}
	}
	changed := map[string]any{}
	signOut := false
	if req.Name != nil {
		u.Name, changed["name"] = strings.TrimSpace(*req.Name), true
	}
	if req.Disabled != nil {
		u.Disabled, changed["disabled"], signOut = *req.Disabled, *req.Disabled, *req.Disabled
	}
	if req.IsOwner != nil {
		u.IsOwner, changed["owner"] = *req.IsOwner, *req.IsOwner
	}
	if req.Password != nil {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			badRequest(w, "%v", err)
			return
		}
		u.PasswordHash, changed["password"], signOut = hash, true, true
	}
	if err := a.Store.UpdateUser(r.Context(), u); err != nil {
		a.internalError(w, err)
		return
	}
	if signOut {
		_ = a.Store.DeleteUserSessions(r.Context(), u.ID, "")
	}
	a.audit(r, p, "user.update", "", u.Email, changed)
	writeJSON(w, http.StatusOK, u)
}

func (a *API) globalAudit(w http.ResponseWriter, r *http.Request, _ *principal) {
	a.writeAudit(w, r, "")
}

func (a *API) system(w http.ResponseWriter, r *http.Request, _ *principal) {
	o := a.Config.Auth.OIDC
	oidc := map[string]any{"enabled": o.Enabled()}
	if o.Enabled() {
		oidc["issuer"] = o.Issuer
	}
	h := a.Health(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"health":         h,
		"version":        h.Version,
		"dataDir":        a.Config.DataDir,
		"automationsDir": a.Config.AutomationsDir,
		"workers":        a.Config.Workers,
		"invalid":        invalidAll(a.Snapshot()),
		"oidc":           oidc,
	})
}

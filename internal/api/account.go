package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

type workspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type meResponse struct {
	ID          string         `json:"id"`
	Email       string         `json:"email"`
	Name        string         `json:"name"`
	IsOwner     bool           `json:"isOwner"`
	TOTPEnabled bool           `json:"totpEnabled"`
	Workspaces  []workspaceRef `json:"workspaces"`
}

func (a *API) meFor(r *http.Request, u *store.User) (meResponse, error) {
	resp := meResponse{ID: u.ID, Email: u.Email, Name: u.Name, IsOwner: u.IsOwner, TOTPEnabled: u.TOTPEnabled, Workspaces: []workspaceRef{}}
	all, err := a.Store.ListWorkspaces(r.Context())
	if err != nil {
		return resp, err
	}
	names := map[string]string{}
	for _, w := range all {
		names[w.ID] = w.Name
	}
	if u.IsOwner {
		for _, w := range all {
			resp.Workspaces = append(resp.Workspaces, workspaceRef{ID: w.ID, Name: w.Name, Role: RoleOwner})
		}
		return resp, nil
	}
	ms, err := a.Store.UserMemberships(r.Context(), u.ID)
	if err != nil {
		return resp, err
	}
	for _, m := range ms {
		resp.Workspaces = append(resp.Workspaces, workspaceRef{ID: m.WorkspaceID, Name: names[m.WorkspaceID], Role: m.Role})
	}
	return resp, nil
}

func (a *API) writeMe(w http.ResponseWriter, r *http.Request, u *store.User) {
	me, err := a.meFor(r, u)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, me)
}

func (a *API) getSetup(w http.ResponseWriter, r *http.Request) {
	n, err := a.Store.CountUsers(r.Context())
	if err != nil {
		a.internalError(w, err)
		return
	}
	label := a.Config.Auth.OIDC.Label
	if label == "" {
		label = "single sign-on"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"needsSetup": n == 0,
		"oidc":       map[string]any{"enabled": a.Config.Auth.OIDC.Enabled(), "label": label},
	})
}

func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && !strings.ContainsAny(s, " \r\n")
}

func (a *API) postSetup(w http.ResponseWriter, r *http.Request) {
	var req struct{ Token, Email, Name, Password string }
	if !decode(w, r, &req) {
		return
	}
	n, err := a.Store.CountUsers(r.Context())
	if err != nil {
		a.internalError(w, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "conflict", "setup is already done; sign in instead")
		return
	}
	if !a.loginByIP.Allow("setup:"+a.clientIP(r), a.Now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; wait 15 minutes")
		return
	}
	if !a.checkSetupToken(strings.TrimSpace(req.Token)) {
		writeError(w, http.StatusUnauthorized, "invalid_setup_token", "wrong setup token; copy it from the server log")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(req.Email) {
		badRequest(w, "invalid email address")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		badRequest(w, "%v", err)
		return
	}
	u := &store.User{Email: req.Email, Name: strings.TrimSpace(req.Name), PasswordHash: hash, IsOwner: true, CreatedAt: a.Now()}
	if err := a.Store.CreateUser(r.Context(), u); err != nil {
		a.internalError(w, err)
		return
	}
	a.SetSetupToken("")
	a.audit(r, &principal{User: u}, "setup.owner", "", u.Email, nil)
	if err := a.startSession(w, r, u); err != nil {
		a.internalError(w, err)
		return
	}
	a.writeMe(w, r, u)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req struct{ Email, Password, Code string }
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	now := a.Now()
	if !a.loginByIP.Allow(a.clientIP(r), now) || !a.loginByEmail.Allow(email, now) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many sign-in attempts; wait 15 minutes")
		return
	}
	u, err := a.Store.GetUserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.internalError(w, err)
		return
	}
	hash := ""
	if u != nil && !u.Disabled {
		hash = u.PasswordHash
	}
	if !auth.CheckPasswordOrDummy(hash, req.Password) {
		a.audit(r, nil, "auth.login_failed", "", email, nil)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong email or password")
		return
	}
	if u.TOTPEnabled {
		if strings.TrimSpace(req.Code) == "" {
			writeError(w, http.StatusUnauthorized, "totp_required", "enter the code from your authenticator app")
			return
		}
		secret, err := a.Vault.Open(u.TOTPSecret, "totp:"+u.ID)
		if err != nil {
			a.internalError(w, err)
			return
		}
		step, ok := auth.CheckTOTP(string(secret), req.Code, now, u.TOTPLast)
		if !ok {
			a.audit(r, nil, "auth.login_failed", "", email, map[string]any{"reason": "totp"})
			writeError(w, http.StatusUnauthorized, "totp_invalid", "wrong or already used code")
			return
		}
		_ = a.Store.SetTOTPLast(r.Context(), u.ID, step)
	}
	a.loginByEmail.Reset(email)
	if err := a.startSession(w, r, u); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, &principal{User: u}, "auth.login", "", u.Email, map[string]any{"method": "password"})
	a.writeMe(w, r, u)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request, p *principal) {
	if p.TokenHash != "" {
		_ = a.Store.DeleteSession(r.Context(), p.TokenHash)
	}
	clearSessionCookie(w)
	a.audit(r, p, "auth.logout", "", p.User.Email, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request, p *principal) {
	a.writeMe(w, r, p.User)
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request, p *principal) {
	var req struct{ Current, New string }
	if !decode(w, r, &req) {
		return
	}
	if p.User.PasswordHash != "" && !auth.CheckPassword(p.User.PasswordHash, req.Current) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "the current password is wrong")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		badRequest(w, "%v", err)
		return
	}
	p.User.PasswordHash = hash
	if err := a.Store.UpdateUser(r.Context(), p.User); err != nil {
		a.internalError(w, err)
		return
	}
	_ = a.Store.DeleteUserSessions(r.Context(), p.User.ID, p.TokenHash)
	a.audit(r, p, "auth.password", "", p.User.Email, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) totpSetup(w http.ResponseWriter, r *http.Request, p *principal) {
	if p.User.TOTPEnabled {
		writeError(w, http.StatusConflict, "conflict", "two-factor authentication is already on; turn it off first")
		return
	}
	secret := auth.NewTOTPSecret()
	sealed, err := a.Vault.Seal([]byte(secret), "totp:"+p.User.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	p.User.TOTPSecret = sealed
	if err := a.Store.UpdateUser(r.Context(), p.User); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": auth.TOTPURI(secret, p.User.Email, "Pendolo")})
}

func (a *API) totpEnable(w http.ResponseWriter, r *http.Request, p *principal) {
	var req struct{ Code string }
	if !decode(w, r, &req) {
		return
	}
	if p.User.TOTPSecret == "" {
		badRequest(w, "start the setup first")
		return
	}
	secret, err := a.Vault.Open(p.User.TOTPSecret, "totp:"+p.User.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	step, ok := auth.CheckTOTP(string(secret), req.Code, a.Now(), 0)
	if !ok {
		writeError(w, http.StatusBadRequest, "totp_invalid", "wrong code; check the time on your phone and try again")
		return
	}
	p.User.TOTPEnabled, p.User.TOTPLast = true, step
	if err := a.Store.UpdateUser(r.Context(), p.User); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "auth.totp_enable", "", p.User.Email, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) totpDisable(w http.ResponseWriter, r *http.Request, p *principal) {
	var req struct{ Password string }
	if !decode(w, r, &req) {
		return
	}
	if !auth.CheckPassword(p.User.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong password")
		return
	}
	p.User.TOTPEnabled, p.User.TOTPSecret, p.User.TOTPLast = false, "", 0
	if err := a.Store.UpdateUser(r.Context(), p.User); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "auth.totp_disable", "", p.User.Email, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listTokens(w http.ResponseWriter, r *http.Request, p *principal) {
	list, err := a.Store.ListAPITokens(r.Context(), p.User.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if list == nil {
		list = []store.APIToken{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request, p *principal) {
	var req struct {
		Name          string
		ExpiresInDays int
	}
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		badRequest(w, "give the token a name of 1 to 100 characters")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		badRequest(w, "expiresInDays must be between 0 (never) and 3650")
		return
	}
	now := a.Now()
	t := store.APIToken{ID: store.NewID(now), UserID: p.User.ID, Name: req.Name, CreatedAt: now}
	if req.ExpiresInDays > 0 {
		exp := now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		t.ExpiresAt = &exp
	}
	secret := auth.Token("pdl_")
	if err := a.Store.CreateAPIToken(r.Context(), t, auth.HashToken(secret)); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "token.create", "", req.Name, nil)
	writeJSON(w, http.StatusCreated, struct {
		store.APIToken
		Token string `json:"token"`
	}{t, secret})
}

func (a *API) deleteToken(w http.ResponseWriter, r *http.Request, p *principal) {
	err := a.Store.DeleteAPIToken(r.Context(), p.User.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "token")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, p, "token.delete", "", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

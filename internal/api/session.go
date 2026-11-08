package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

const sessionCookie = "pendolo_session"

// principal is who is calling.
type principal struct {
	User      *store.User
	TokenName string // set for API tokens
	TokenHash string // session token hash, for sign-out
}

func (p *principal) label() string {
	if p.TokenName != "" {
		return p.User.Email + " (api-token:" + p.TokenName + ")"
	}
	return p.User.Email
}

// authenticate resolves the caller from a bearer token or the session
// cookie. It returns nil when neither is valid.
func (a *API) authenticate(r *http.Request) (*principal, error) {
	ctx := r.Context()
	now := a.Now()
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		tok, err := a.Store.GetAPITokenByHash(ctx, auth.HashToken(strings.TrimSpace(bearer)), now)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		u, err := a.activeUser(ctx, tok.UserID)
		if u == nil || err != nil {
			return nil, err
		}
		if tok.LastUsedAt == nil || now.Sub(*tok.LastUsedAt) > time.Minute {
			_ = a.Store.TouchAPIToken(ctx, tok.ID, now)
		}
		return &principal{User: u, TokenName: tok.Name}, nil
	}

	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	hash := auth.HashToken(c.Value)
	sess, err := a.Store.GetSession(ctx, hash, now)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u, err := a.activeUser(ctx, sess.UserID)
	if u == nil || err != nil {
		return nil, err
	}
	// Slide the expiry at most once an hour, to keep writes rare.
	if now.Sub(sess.LastSeen) > time.Hour {
		_ = a.Store.ExtendSession(ctx, hash, now, now.Add(a.Config.Auth.SessionTTL.Std()))
	}
	return &principal{User: u, TokenHash: hash}, nil
}

func (a *API) activeUser(ctx context.Context, id string) (*store.User, error) {
	u, err := a.Store.GetUser(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil || u.Disabled {
		return nil, err
	}
	return u, nil
}

func (a *API) requireUser(h func(http.ResponseWriter, *http.Request, *principal), ownerOnly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.authenticate(r)
		if err != nil {
			a.internalError(w, err)
			return
		}
		if p == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in first")
			return
		}
		if ownerOnly && !p.User.IsOwner {
			writeError(w, http.StatusForbidden, "forbidden", "only owners can do this")
			return
		}
		h(w, r, p)
	})
}

// requireWorkspace checks the caller's role in {ws}. Non-members get 404, so
// the existence of other workspaces never leaks.
func (a *API) requireWorkspace(perm string, h func(http.ResponseWriter, *http.Request, *principal, string)) http.Handler {
	return a.requireUser(func(w http.ResponseWriter, r *http.Request, p *principal) {
		ws := r.PathValue("ws")
		role, err := a.roleIn(r.Context(), p, ws)
		if err != nil {
			a.internalError(w, err)
			return
		}
		if role == "" {
			notFound(w, "workspace")
			return
		}
		if !can(role, perm) {
			writeError(w, http.StatusForbidden, "forbidden", "your role in this workspace ("+role+") does not allow this")
			return
		}
		h(w, r, p, ws)
	}, false)
}

func (a *API) roleIn(ctx context.Context, p *principal, ws string) (string, error) {
	if p.User.IsOwner {
		for _, w := range a.workspaces(ctx) {
			if w.ID == ws {
				return RoleOwner, nil
			}
		}
		return "", nil
	}
	return a.Store.Role(ctx, p.User.ID, ws)
}

func (a *API) workspaces(ctx context.Context) []store.Workspace {
	list, err := a.Store.ListWorkspaces(ctx)
	if err != nil {
		a.Log.Error("list workspaces", "err", err)
	}
	return list
}

// startSession signs the user in on this browser.
func (a *API) startSession(w http.ResponseWriter, r *http.Request, u *store.User) error {
	token := auth.Token("pds_")
	now := a.Now()
	ttl := a.Config.Auth.SessionTTL.Std()
	if err := a.Store.CreateSession(r.Context(), auth.HashToken(token), u.ID, a.clientIP(r), r.UserAgent(), now, now.Add(ttl)); err != nil {
		return err
	}
	_ = a.Store.TouchLogin(r.Context(), u.ID, now)
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request (TLS or a trusted proxy); plain HTTP works on localhost
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: a.secureRequest(r), MaxAge: int(ttl.Seconds()),
	})
	return nil
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/enthonysclafani/automatizzatore/internal/auth"
	"github.com/enthonysclafani/automatizzatore/internal/secrets"
	"github.com/enthonysclafani/automatizzatore/internal/store"
)

const oidcCookie = "pendolo_oidc"

type oidcProvider struct {
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
}

// SecretsProvider resolves secret:// references in the configuration.
var SecretsProvider func() secrets.Provider

// provider discovers the OpenID provider on first use, so Pendolo starts
// even when the provider is unreachable.
func (a *API) provider(ctx context.Context) (*oidcProvider, error) {
	a.oidcMu.Lock()
	defer a.oidcMu.Unlock()
	if a.oidcProv != nil {
		return a.oidcProv, nil
	}
	cfg := a.Config.Auth.OIDC
	clientSecret := cfg.ClientSecret
	if SecretsProvider != nil {
		v, err := secrets.NewResolver(SecretsProvider()).String(clientSecret)
		if err != nil {
			return nil, fmt.Errorf("auth.oidc.clientSecret: %w", err)
		}
		clientSecret = v
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", cfg.Issuer, err)
	}
	a.oidcProv = &oidcProvider{
		verifier: p.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: clientSecret, Endpoint: p.Endpoint(),
			RedirectURL: cfg.RedirectURL, Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
		},
	}
	return a.oidcProv, nil
}

func loginRedirect(w http.ResponseWriter, r *http.Request, problem string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(problem), http.StatusFound)
}

func (a *API) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if !a.Config.Auth.OIDC.Enabled() {
		notFound(w, "single sign-on")
		return
	}
	p, err := a.provider(r.Context())
	if err != nil {
		a.Log.Error("oidc", "err", err)
		loginRedirect(w, r, "The sign-in provider is unreachable. Try again or use your password.")
		return
	}
	state, nonce, verifier := auth.Token(""), auth.Token(""), oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request: plain HTTP works on localhost
		Name: oidcCookie, Value: state + "." + nonce + "." + verifier, Path: "/api/v1/auth/oidc/",
		HttpOnly: true, Secure: a.secureRequest(r), SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	http.Redirect(w, r, p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (a *API) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if !a.Config.Auth.OIDC.Enabled() {
		notFound(w, "single sign-on")
		return
	}
	c, err := r.Cookie(oidcCookie)
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: "/api/v1/auth/oidc/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	parts := []string{}
	if err == nil {
		parts = strings.Split(c.Value, ".")
	}
	if len(parts) != 3 || r.URL.Query().Get("state") != parts[0] || parts[0] == "" {
		loginRedirect(w, r, "The sign-in expired or came from another browser. Try again.")
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		loginRedirect(w, r, "The provider refused the sign-in: "+e)
		return
	}
	p, err := a.provider(r.Context())
	if err != nil {
		a.Log.Error("oidc", "err", err)
		loginRedirect(w, r, "The sign-in provider is unreachable.")
		return
	}
	tok, err := p.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(parts[2]))
	if err != nil {
		a.Log.Warn("oidc exchange", "err", err)
		loginRedirect(w, r, "The sign-in could not be completed. Try again.")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idToken, err := p.verifier.Verify(r.Context(), raw)
	if err != nil || idToken.Nonce != parts[1] {
		a.Log.Warn("oidc id token", "err", err)
		loginRedirect(w, r, "The sign-in could not be verified. Try again.")
		return
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Email == "" {
		loginRedirect(w, r, "The provider did not share an email address.")
		return
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		loginRedirect(w, r, "Verify your email address with the provider first.")
		return
	}
	u, problem := a.oidcUser(r, strings.ToLower(claims.Email), claims.Name)
	if u == nil {
		a.audit(r, nil, "auth.login_failed", "", claims.Email, map[string]any{"method": "oidc", "reason": problem})
		loginRedirect(w, r, problem)
		return
	}
	if err := a.startSession(w, r, u); err != nil {
		a.internalError(w, err)
		return
	}
	a.audit(r, &principal{User: u}, "auth.login", "", u.Email, map[string]any{"method": "oidc"})
	http.Redirect(w, r, "/", http.StatusFound)
}

// oidcUser finds the user, or creates one when the configuration allows.
// Without a user it returns the message to show on the login page.
func (a *API) oidcUser(r *http.Request, email, name string) (*store.User, string) {
	u, err := a.Store.GetUserByEmail(r.Context(), email)
	switch {
	case err == nil && u.Disabled:
		return nil, "This account is disabled."
	case err == nil:
		return u, ""
	case !errors.Is(err, store.ErrNotFound):
		a.Log.Error("oidc user lookup", "err", err)
		return nil, "Internal error; see the server log."
	}
	cfg := a.Config.Auth.OIDC
	_, domain, _ := strings.Cut(email, "@")
	allowed := false
	for _, d := range cfg.AllowedDomains {
		allowed = allowed || strings.EqualFold(strings.TrimPrefix(d, "@"), domain)
	}
	if !cfg.AutoCreate || !allowed {
		return nil, "No account for " + email + ". Ask an owner to add you."
	}
	u = &store.User{Email: email, Name: name, CreatedAt: a.Now()}
	if err := a.Store.CreateUser(r.Context(), u); err != nil {
		a.Log.Error("oidc create user", "err", err)
		return nil, "Internal error; see the server log."
	}
	a.audit(r, &principal{User: u}, "user.create", "", email, map[string]any{"via": "oidc"})
	return u, ""
}

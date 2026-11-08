package api

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/enthonysclafani/automatizzatore/internal/config"
)

// fakeIssuer is a minimal OpenID provider that signs real ID tokens.
type fakeIssuer struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	nonce string
	email string
	// unverified leaves email_verified out of the ID token.
	unverified bool
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{key: key, email: "new.person@example.com"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("code_verifier") == "" {
			http.Error(w, "missing PKCE verifier", http.StatusBadRequest)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		c := map[string]any{
			"iss": f.srv.URL, "sub": "user-1", "aud": "pendolo", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"nonce": f.nonce, "email": f.email, "email_verified": true, "name": "New Person",
		}
		if f.unverified {
			delete(c, "email_verified")
		}
		claims, _ := json.Marshal(c)
		jws, _ := signer.Sign(claims)
		idToken, _ := jws.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idToken, "expires_in": 3600})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestOIDCLogin(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now() // ID tokens are checked against the real clock
	e.setupOwner()
	issuer := newFakeIssuer(t)
	e.api.Config.Auth.OIDC = config.OIDC{
		Issuer: issuer.srv.URL, ClientID: "pendolo", ClientSecret: "s", RedirectURL: e.srv.URL + "/api/v1/auth/oidc/callback",
		AutoCreate: true, AllowedDomains: []string{"example.com"},
	}

	signIn := func() (*client, string) {
		c := e.browser()
		c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := c.http.Get(e.srv.URL + "/api/v1/auth/oidc/login")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		auth, _ := url.Parse(resp.Header.Get("Location"))
		q := auth.Query()
		if !strings.HasPrefix(auth.String(), issuer.srv.URL+"/authorize") || q.Get("code_challenge") == "" {
			t.Fatalf("login redirect: %s", auth)
		}
		issuer.nonce = q.Get("nonce")
		resp, err = c.http.Get(e.srv.URL + "/api/v1/auth/oidc/callback?code=abc&state=" + url.QueryEscape(q.Get("state")))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return c, resp.Header.Get("Location")
	}

	c, where := signIn()
	if where != "/" {
		t.Fatalf("callback redirected to %q", where)
	}
	var me meResponse
	c.do("GET", "/api/v1/auth/me", nil, &me)
	if me.Email != "new.person@example.com" || me.IsOwner || len(me.Workspaces) != 0 {
		t.Fatalf("auto-created user: %+v", me)
	}

	// An email the provider does not mark as verified opens no account,
	// not even an existing one.
	issuer.unverified, issuer.email = true, "owner@example.com"
	if _, where = signIn(); !strings.Contains(where, "verified") {
		t.Fatalf("unverified email redirected to %q", where)
	}
	issuer.unverified = false

	// A domain that is not allowed gets no account.
	issuer.email = "stranger@other.org"
	_, where = signIn()
	if !strings.HasPrefix(where, "/login?error=") || !strings.Contains(where, "stranger") {
		t.Fatalf("stranger redirected to %q", where)
	}

	// A tampered state is refused.
	c2 := e.browser()
	c2.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := c2.http.Get(e.srv.URL + "/api/v1/auth/oidc/callback?code=abc&state=forged")
	resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Location"), "/login?error=") {
		t.Fatalf("forged state: %s", resp.Header.Get("Location"))
	}
}

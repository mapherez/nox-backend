// Package testauth supplies a controlled identity provider for Backend acceptance
// tests. It is not imported by the production binary or copied into its image.
package testauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	noxauth "github.com/mapherez/nox-auth/client"
)

const Issuer = "https://auth.fixture.test"

type code struct{ ClientID, Callback, Nonce, Challenge string }
type issued struct {
	ClientID string
	Identity noxauth.Identity
}
type Provider struct {
	DeniedEmails map[string]bool
	mu           sync.Mutex
	key          *ecdsa.PrivateKey
	clients      map[string]string
	codes        map[string]code
	tokens       map[string]issued
	denied       bool
	checks       int
	logoutScope  string
	unavailable  bool
}

func New() (*Provider, error) {
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	return &Provider{key: k, clients: map[string]string{}, codes: map[string]code{}, tokens: map[string]issued{}}, nil
}
func (p *Provider) Register(id, secret string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[id] = secret
}
func (p *Provider) Deny()               { p.mu.Lock(); defer p.mu.Unlock(); p.denied = true }
func (p *Provider) Unavailable()        { p.mu.Lock(); defer p.mu.Unlock(); p.unavailable = true }
func (p *Provider) LogoutScope() string { p.mu.Lock(); defer p.mu.Unlock(); return p.logoutScope }
func (p *Provider) Checks() int         { p.mu.Lock(); defer p.mu.Unlock(); return p.checks }
func (p *Provider) Issue(id, email string) noxauth.Credentials {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.issue(id, email)
}
func (p *Provider) issue(id, email string) noxauth.Credentials {
	identity := noxauth.Identity{Subject: "fixture-subject", Email: email, EmailVerified: true, Name: "Fixture", Admin: true, GoogleSubject: "fixture-google-subject", GoogleIssuer: "https://accounts.google.com"}
	token := noxauth.GenerateSecret()
	p.tokens[token] = issued{id, identity}
	return noxauth.Credentials{AccessToken: token, Identity: identity, ExpiresAt: time.Now().Add(365 * 24 * time.Hour), BrowserRenewAt: time.Now().Add(30 * 24 * time.Hour)}
}
func (p *Provider) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if p.unavailable {
			http.Error(w, "fixture unavailable", 503)
			return
		}
		switch r.URL.Path {
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "fixture-key", Algorithm: "ES256", Use: "sig"}}})
			return
		case "/authorize":
			q := r.URL.Query()
			id := q.Get("client_id")
			if p.clients[id] == "" || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
				http.Error(w, "invalid fixture client", 400)
				return
			}
			key := noxauth.GenerateSecret()
			p.codes[key] = code{id, q.Get("redirect_uri"), q.Get("nonce"), q.Get("code_challenge")}
			target, e := url.Parse(q.Get("redirect_uri"))
			if e != nil {
				http.Error(w, "invalid fixture callback", 400)
				return
			}
			query := target.Query()
			query.Set("code", key)
			query.Set("state", q.Get("state"))
			target.RawQuery = query.Encode()
			http.Redirect(w, r, target.String(), 302)
			return
		}
		id, secret, ok := r.BasicAuth()
		if !ok || p.clients[id] != secret {
			http.Error(w, "fixture client denied", 401)
			return
		}
		if r.URL.Path == "/v1/admission" {
			var input struct {
				Email string `json:"email"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				http.Error(w, "bad request", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": !p.denied && !p.DeniedEmails[input.Email]})
			return
		}
		if r.URL.Path == "/v1/logout" {
			var input struct{ Token, Scope string }
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				http.Error(w, "invalid fixture logout", 400)
				return
			}
			p.logoutScope = input.Scope
			delete(p.tokens, input.Token)
			w.WriteHeader(204)
			return
		}
		if r.ParseForm() != nil {
			http.Error(w, "invalid fixture request", 400)
			return
		}
		switch r.URL.Path {
		case "/oauth/introspect":
			p.checks++
			token, found := p.tokens[r.Form.Get("token")]
			if !found || token.ClientID != id || p.denied {
				_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
				return
			}
			claims := map[string]any{"active": true, "client_id": id, "exp": time.Now().Add(365 * 24 * time.Hour).Unix(), "sub": token.Identity.Subject, "email": token.Identity.Email, "email_verified": true, "name": "Fixture", "nox_admin": true, "google_sub": "fixture-google-subject", "google_iss": "https://accounts.google.com"}
			_ = json.NewEncoder(w).Encode(claims)
		case "/oauth/token":
			key := r.Form.Get("code")
			req, found := p.codes[key]
			delete(p.codes, key)
			if !found || req.ClientID != id || req.Callback != r.Form.Get("redirect_uri") || req.Challenge != noxauth.PKCEChallenge(r.Form.Get("code_verifier")) || p.denied {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			credentials := p.issue(id, "owner@example.test")
			h := sha256.Sum256([]byte(credentials.AccessToken))
			claims := map[string]any{"iss": Issuer, "sub": credentials.Identity.Subject, "aud": id, "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": req.Nonce, "at_hash": base64.RawURLEncoding.EncodeToString(h[:16]), "email": credentials.Identity.Email, "email_verified": true, "nox_admin": true, "google_sub": "fixture-google-subject", "google_iss": "https://accounts.google.com"}
			signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: &jose.JSONWebKey{Key: p.key, KeyID: "fixture-key"}}, (&jose.SignerOptions{}).WithType("JWT"))
			if e != nil {
				http.Error(w, "signing failed", 500)
				return
			}
			body, _ := json.Marshal(claims)
			signed, e := signer.Sign(body)
			if e != nil {
				http.Error(w, "signing failed", 500)
				return
			}
			raw, e := signed.CompactSerialize()
			if e != nil {
				http.Error(w, "signing failed", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": credentials.AccessToken, "id_token": raw, "token_type": "Bearer", "expires_in": 365 * 24 * 3600})
		default:
			http.NotFound(w, r)
		}
	})
}

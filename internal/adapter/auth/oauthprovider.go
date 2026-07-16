package auth

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/instancez/instancez/internal/domain"
)

// OAuthProvider is the seam for an external identity provider. Adding a provider
// is one implementation of this interface plus a RegisterOAuth call, with no
// handler or config-struct edits.
type OAuthProvider interface {
	Name() string
	AuthorizeURL(cfg *domain.OAuthProvider, state string) string
	ExchangeCode(cfg *domain.OAuthProvider, code string) (accessToken string, err error)
	FetchUser(accessToken string) (*OAuthUserInfo, error)
}

var (
	oauthOnce     sync.Once
	oauthRegistry = map[string]OAuthProvider{}
)

func registerOAuthBuiltins() {
	oauthOnce.Do(func() {
		RegisterOAuth(googleProvider{})
		RegisterOAuth(&githubProvider{
			userAPI:  "https://api.github.com/user",
			emailAPI: "https://api.github.com/user/emails",
		})
	})
}

// RegisterOAuth adds a provider to the registry, keyed by Name().
func RegisterOAuth(p OAuthProvider) { oauthRegistry[p.Name()] = p }

// OAuthRegistry returns the provider registered under name. The built-in
// google/github providers are registered on first use.
func OAuthRegistry(name string) (OAuthProvider, bool) {
	registerOAuthBuiltins()
	p, ok := oauthRegistry[name]
	return p, ok
}

// ---- google ----

type googleProvider struct{}

func (googleProvider) Name() string { return "google" }

func (googleProvider) AuthorizeURL(cfg *domain.OAuthProvider, state string) string {
	return fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid+email+profile&state=%s",
		cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
}

func (googleProvider) ExchangeCode(cfg *domain.OAuthProvider, code string) (string, error) {
	return exchangeOAuthCode("https://oauth2.googleapis.com/token", cfg, code)
}

func (googleProvider) FetchUser(accessToken string) (*OAuthUserInfo, error) {
	return fetchGoogleUser(accessToken)
}

// ---- github ----

// githubProvider holds its API endpoints as fields so tests can point them at a
// local httptest server.
type githubProvider struct {
	userAPI  string
	emailAPI string
}

func (githubProvider) Name() string { return "github" }

func (githubProvider) AuthorizeURL(cfg *domain.OAuthProvider, state string) string {
	return fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=user:email&state=%s",
		cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
}

func (githubProvider) ExchangeCode(cfg *domain.OAuthProvider, code string) (string, error) {
	return exchangeOAuthCode("https://github.com/login/oauth/access_token", cfg, code)
}

func (g *githubProvider) FetchUser(accessToken string) (*OAuthUserInfo, error) {
	return fetchGitHubUser(g, accessToken)
}

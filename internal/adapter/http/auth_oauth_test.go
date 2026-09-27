package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	adapterauth "github.com/instancez/instancez/internal/adapter/auth"
	"github.com/instancez/instancez/internal/domain"
)

type unitOAuthProvider struct{}

func (unitOAuthProvider) Name() string                                      { return "unitfake" }
func (unitOAuthProvider) AuthorizeURL(*domain.OAuthProvider, string) string { return "http://idp" }
func (unitOAuthProvider) ExchangeCode(*domain.OAuthProvider, string) (string, error) {
	return "tok", nil
}
func (unitOAuthProvider) FetchUser(string) (*adapterauth.OAuthUserInfo, error) {
	return &adapterauth.OAuthUserInfo{ProviderID: "p1", Email: "v@e.com", Name: "V", EmailVerified: false}, nil
}

func TestEmailVerifiedClaim(t *testing.T) {
	for in, want := range map[any]bool{true: true, "true": true, false: false, "false": false, "TRUE": false, "": false, 1: false} {
		if got := emailVerifiedClaim(in); got != want {
			t.Errorf("%v: got %v", in, got)
		}
	}
	if emailVerifiedClaim(nil) {
		t.Error("missing claim must be unverified")
	}
}

func TestOAuthLoginError(t *testing.T) {
	cases := []struct {
		err        error
		status     int
		code, frag string
	}{
		{domain.ErrSignupDisabled, 403, "signup_disabled", "Signups not allowed"},
		{domain.ErrProviderEmailUnverified, 422, "provider_email_needs_verification", "Unverified email with google"},
		{domain.ErrOAuthLinkRefused, 422, "email_exists", "confirm your email"},
		{errors.New("db"), 500, "internal", "Failed to create or find user"},
		{nil, 500, "internal", "Failed to create or find user"},
	}
	for _, tc := range cases {
		st, code, msg := oauthLoginError(tc.err, "google")
		if st != tc.status || code != tc.code || !strings.Contains(msg, tc.frag) {
			t.Errorf("%v: got %d %s %q", tc.err, st, code, msg)
		}
	}
}

func TestOAuthLoginError_CodesAreStableSlugs(t *testing.T) {
	for _, slug := range []string{"provider_email_needs_verification", "email_exists"} {
		if errTypeToCode[slug] != slug {
			t.Errorf("%s maps to %q", slug, errTypeToCode[slug])
		}
	}
}

func TestOAuthCallback_PassesVerificationAndSignupPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	no := false
	for name, allow := range map[string]*bool{"signups closed": &no, "signups default": nil} {
		var got domain.OAuthLogin
		h := &AuthHandler{
			cfg: &domain.Config{Auth: &domain.Auth{AllowSignup: allow, RedirectURLs: []string{"http://app.local"},
				OAuth: map[string]*domain.OAuthProvider{"unitfake": {ClientID: "c", RedirectURL: "http://api/cb"}}}},
			logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t),
			authSvc: &stubAuthService{
				consumeOAuthFlowFn: func(context.Context, string) (domain.FlowState, error) {
					return domain.FlowState{RedirectTo: "http://app.local/cb"}, nil
				},
				upsertOAuthUserFn: func(ctx context.Context, in domain.OAuthLogin) (map[string]any, error) {
					got = in
					return nil, domain.ErrProviderEmailUnverified
				},
			},
		}
		r := gin.New()
		r.GET("/cb", h.handleOAuthCallback("unitfake"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/cb?state=s&code=c", nil))
		if got.EmailVerified || got.AllowSignup != (allow == nil) || got.ProviderUserID != "p1" || got.Email != "v@e.com" || got.Provider != "unitfake" {
			t.Fatalf("%s: login passed to service = %+v", name, got)
		}
		loc := w.Header().Get("Location")
		if w.Code != 302 || !strings.Contains(loc, "error_description=Unverified+email") || strings.Contains(loc, "access_token") {
			t.Fatalf("%s: status %d location %q", name, w.Code, loc)
		}
	}
}

func TestIDTokenGrant_PassesEmailVerifiedClaim(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	providerJWKS.mu.Lock()
	providerJWKS.cache["google"] = &jwksCache{keys: map[string]*rsa.PublicKey{"k1": &key.PublicKey}, fetchedAt: time.Now()}
	providerJWKS.mu.Unlock()
	t.Cleanup(func() {
		providerJWKS.mu.Lock()
		delete(providerJWKS.cache, "google")
		providerJWKS.mu.Unlock()
	})

	for _, verified := range []any{false, true, "true", nil} {
		claims := jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "cid", "sub": "g-1", "email": "v@e.com", "exp": time.Now().Add(time.Minute).Unix()}
		if verified != nil {
			claims["email_verified"] = verified
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "k1"
		signed, _ := tok.SignedString(key)

		var got domain.OAuthLogin
		h := &AuthHandler{
			cfg:    &domain.Config{Auth: &domain.Auth{OAuth: map[string]*domain.OAuthProvider{"google": {ClientID: "cid"}}}},
			logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t),
			authSvc: &stubAuthService{upsertOAuthUserFn: func(ctx context.Context, in domain.OAuthLogin) (map[string]any, error) {
				got = in
				return nil, domain.ErrProviderEmailUnverified
			}},
		}
		r := gin.New()
		r.POST("/token", h.handleIDTokenGrant)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/token", strings.NewReader(`{"provider":"google","token":"`+signed+`"}`)))
		want := verified == true || verified == "true"
		if got.EmailVerified != want || !got.AllowSignup || got.ProviderUserID != "g-1" || got.Email != "v@e.com" {
			t.Fatalf("email_verified=%v: login passed to service = %+v", verified, got)
		}
		if w.Code != 422 || !strings.Contains(w.Body.String(), "provider_email_needs_verification") {
			t.Fatalf("email_verified=%v: status %d body %s", verified, w.Code, w.Body.String())
		}
	}
}

func linkHandler(t *testing.T, svc *stubAuthService) *AuthHandler {
	return &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{RedirectURLs: []string{"http://app.local"},
			OAuth: map[string]*domain.OAuthProvider{"unitfake": {ClientID: "c", RedirectURL: "http://api/cb"}}}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t), authSvc: svc,
	}
}

func responseCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range (&http.Response{Header: w.Header()}).Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func TestLinkIdentity_SetsBindingCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	cases := []struct {
		name, base, target, cookie string
		secure                     bool
	}{
		{"https base url", "https://app.example.com", "http://api/link?provider=unitfake", "__Host-oauth_link_state", true},
		{"tls request", "http://localhost:8080", "https://api/link?provider=unitfake", "__Host-oauth_link_state", true},
		{"plain http dev", "http://localhost:8080", "http://api/link?provider=unitfake", "oauth_link_state", false},
	}
	for _, tc := range cases {
		t.Setenv("INSTANCEZ_BASE_URL", tc.base)
		var state, linking string
		h := linkHandler(t, &stubAuthService{createOAuthFlowFn: func(_ context.Context, s, _, _, _, uid string) error {
			state, linking = s, uid
			return nil
		}})
		r := gin.New()
		r.GET("/link", func(c *gin.Context) { c.Set(contextKeySession, domain.Session{UserID: "u-1"}) }, h.handleLinkIdentity)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", tc.target, nil))
		if w.Code != 200 || linking != "u-1" || state == "" {
			t.Fatalf("%s: status %d linking %q state %q", tc.name, w.Code, linking, state)
		}
		cookies := (&http.Response{Header: w.Header()}).Cookies()
		ck := responseCookie(w, tc.cookie)
		if len(cookies) != 1 || ck == nil || ck.Value != state || ck.Path != "/" || ck.Domain != "" || ck.MaxAge != 600 ||
			!ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Secure != tc.secure {
			t.Fatalf("%s: cookies %+v", tc.name, cookies)
		}
	}

	h := linkHandler(t, &stubAuthService{createOAuthFlowFn: func(context.Context, string, string, string, string, string) error {
		return errors.New("db down")
	}})
	r := gin.New()
	r.GET("/link", func(c *gin.Context) { c.Set(contextKeySession, domain.Session{UserID: "u-1"}) }, h.handleLinkIdentity)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/link?provider=unitfake", nil))
	if w.Code != 500 || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("store failure: status %d cookies %v", w.Code, w.Header().Values("Set-Cookie"))
	}
}

func TestOAuthCallback_LinkRequiresInitiatorCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	const host, bare = "__Host-oauth_link_state", "oauth_link_state"
	cases := []struct {
		name, base, cookieName, cookie string
		linked                         bool
	}{
		{"no cookie", "http://localhost:8080", "", "", false},
		{"empty cookie", "http://localhost:8080", bare, "", false},
		{"wrong cookie", "http://localhost:8080", bare, "other-state", false},
		{"prefix of state", "http://localhost:8080", bare, "link-stat", false},
		{"unicode cookie", "http://localhost:8080", bare, "l%C3%AFnk-state", false},
		{"http matching cookie", "http://localhost:8080", bare, "link-state", true},
		{"https matching __Host- cookie", "https://app.instancez.app", host, "link-state", true},
		{"https tossed bare cookie", "https://app.instancez.app", bare, "link-state", false},
		{"https no cookie", "https://app.instancez.app", "", "", false},
		{"http ignores __Host- cookie", "http://localhost:8080", host, "link-state", false},
	}
	for _, tc := range cases {
		t.Setenv("INSTANCEZ_BASE_URL", tc.base)
		var linkedUser string
		upserted := false
		h := linkHandler(t, &stubAuthService{
			consumeOAuthFlowFn: func(context.Context, string) (domain.FlowState, error) {
				return domain.FlowState{RedirectTo: "http://app.local/cb", LinkingUserID: "attacker"}, nil
			},
			linkIdentityFn: func(_ context.Context, uid, _, _, _ string) { linkedUser = uid },
			upsertOAuthUserFn: func(context.Context, domain.OAuthLogin) (map[string]any, error) {
				upserted = true
				return map[string]any{"id": "x"}, nil
			},
		})
		r := gin.New()
		r.GET("/cb", h.handleOAuthCallback("unitfake"))
		req := httptest.NewRequest("GET", "/cb?state=link-state&code=c", nil)
		if tc.cookieName != "" {
			req.AddCookie(&http.Cookie{Name: tc.cookieName, Value: tc.cookie, HttpOnly: true, Secure: true})
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		loc := w.Header().Get("Location")
		if upserted {
			t.Fatalf("%s: a link flow must never fall through to login", tc.name)
		}
		if tc.linked {
			if linkedUser != "attacker" || w.Code != 302 || loc != "http://app.local/cb#message=identity_linked" {
				t.Fatalf("%s: linked %q status %d loc %q", tc.name, linkedUser, w.Code, loc)
			}
		} else if linkedUser != "" || w.Code != 302 || !strings.Contains(loc, "error=") || strings.Contains(loc, "identity_linked") {
			t.Fatalf("%s: linked %q status %d loc %q", tc.name, linkedUser, w.Code, loc)
		}
		want := bare
		if strings.HasPrefix(tc.base, "https://") {
			want = host
		}
		if ck := responseCookie(w, want); ck == nil || ck.MaxAge >= 0 {
			t.Fatalf("%s: binding cookie %s not cleared: %+v", tc.name, want, ck)
		}
	}
}

func TestOAuthCallback_UnboundLinkWithoutRedirectIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	t.Setenv("INSTANCEZ_BASE_URL", "http://localhost:8080")
	linked := false
	h := linkHandler(t, &stubAuthService{
		consumeOAuthFlowFn: func(context.Context, string) (domain.FlowState, error) {
			return domain.FlowState{LinkingUserID: "attacker"}, nil
		},
		linkIdentityFn: func(context.Context, string, string, string, string) { linked = true },
	})
	h.cfg.Auth.RedirectURLs = nil
	r := gin.New()
	r.GET("/cb", h.handleOAuthCallback("unitfake"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/cb?state=link-state&code=c", nil))
	if linked || w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"bad_oauth_state"`) {
		t.Fatalf("linked=%v status %d body %s", linked, w.Code, w.Body.String())
	}
}

func TestOAuthCallback_LoginFlowIgnoresLinkCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	linked := false
	h := linkHandler(t, &stubAuthService{
		consumeOAuthFlowFn: func(context.Context, string) (domain.FlowState, error) {
			return domain.FlowState{RedirectTo: "http://app.local/cb"}, nil
		},
		linkIdentityFn: func(context.Context, string, string, string, string) { linked = true },
		upsertOAuthUserFn: func(context.Context, domain.OAuthLogin) (map[string]any, error) {
			return nil, domain.ErrOAuthLinkRefused
		},
	})
	r := gin.New()
	r.GET("/cb", h.handleOAuthCallback("unitfake"))
	req := httptest.NewRequest("GET", "/cb?state=s&code=c", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_link_state", Value: "s", HttpOnly: true, Secure: true})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if linked || !strings.Contains(w.Header().Get("Location"), "confirm+your+email") {
		t.Fatalf("linked=%v loc %q", linked, w.Header().Get("Location"))
	}
}

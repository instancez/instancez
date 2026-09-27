package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"log/slog"
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
		{domain.ErrOAuthLinkRefused, 422, "email_exists", "sign in to it first"},
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

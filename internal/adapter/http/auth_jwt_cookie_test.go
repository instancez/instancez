package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	adapterauth "github.com/instancez/instancez/internal/adapter/auth"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func rs512Token(t *testing.T, km *app.JWTKeyManager, claims jwt.MapClaims) string {
	t.Helper()
	key, _ := km.Active(context.Background())
	tok := jwt.NewWithClaims(jwt.SigningMethodRS512, claims)
	tok.Header["kid"] = key.KID
	s, err := tok.SignedString(key.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestJWTAuth_RejectsUnpinnedAlgorithm(t *testing.T) {
	km := stubKeys(t)
	if code := probe(km, rs512Token(t, km, authClaims())).Code; code != 401 {
		t.Fatalf("RS512 with a valid key must be rejected, got %d", code)
	}
	if code := probe(km, signToken(t, km, authClaims())).Code; code != 200 {
		t.Fatalf("RS256 regression: got %d", code)
	}
}

func TestTokenVerify_SharesMiddlewareVerifier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{}}, jwtKeys: km, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := gin.New()
	r.POST("/token/verify", h.handleTokenVerify)
	expired := authClaims()
	expired["exp"] = time.Now().Add(-time.Hour).Unix()
	noExp := authClaims()
	delete(noExp, "exp")
	for name, tc := range map[string]struct {
		tok  string
		want int
	}{
		"valid":   {signToken(t, km, authClaims()), 200},
		"expired": {signToken(t, km, expired), 401},
		"no exp":  {signToken(t, km, noExp), 401},
		"rs512":   {rs512Token(t, km, authClaims()), 401},
		"garbage": {"a.b.c", 401},
	} {
		if w := postJSON(r, "/token/verify", `{"token":"`+tc.tok+`"}`); w.Code != tc.want {
			t.Errorf("%s: status %d want %d", name, w.Code, tc.want)
		}
	}
}

func TestAuthorize_OAuthCookiesSecureOnHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	cases := []struct {
		name, base, target string
		secure             bool
	}{
		{"https base url", "https://app.example.com", "http://api/authorize?provider=unitfake", true},
		{"tls request", "http://localhost:8080", "https://api/authorize?provider=unitfake", true},
		{"plain http dev", "http://localhost:8080", "http://api/authorize?provider=unitfake", false},
	}
	for _, tc := range cases {
		t.Setenv("INSTANCEZ_BASE_URL", tc.base)
		h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{
			OAuth: map[string]*domain.OAuthProvider{"unitfake": {ClientID: "c", RedirectURL: "http://api/cb"}}}},
			authSvc: &stubAuthService{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		r := gin.New()
		r.GET("/authorize", h.handleAuthorize)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", tc.target, nil))
		cookies := (&http.Response{Header: w.Header()}).Cookies()
		if len(cookies) != 2 {
			t.Fatalf("%s: want 2 cookies, got %v", tc.name, cookies)
		}
		for _, ck := range cookies {
			if ck.Secure != tc.secure || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
				t.Errorf("%s: cookie %s secure=%v httponly=%v samesite=%v", tc.name, ck.Name, ck.Secure, ck.HttpOnly, ck.SameSite)
			}
		}
	}
}

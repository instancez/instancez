package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp/totp"

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

func TestAuthorize_StateStoreFailureIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	h := linkHandler(t, &stubAuthService{createOAuthFlowFn: func(context.Context, string, string, string, string, string) error {
		return errors.New("db down")
	}})
	r := gin.New()
	r.GET("/authorize", h.handleAuthorize)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "http://api/authorize?provider=unitfake", nil))
	if w.Code != 500 || w.Header().Get("Location") != "" {
		t.Fatalf("status %d loc %q", w.Code, w.Header().Get("Location"))
	}
}

// Regression: callback on a different host than authorize, with no cookies, must succeed.
func TestOAuthCallback_StateFromDBNeedsNoCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(unitOAuthProvider{})
	cases := []struct {
		name, state string
		known       bool
		wantStatus  int
	}{
		{"known state, no cookie", "good", true, 302},
		{"unknown state", "forged", false, 400},
		{"empty state", "", false, 400},
	}
	for _, tc := range cases {
		h := linkHandler(t, &stubAuthService{consumeOAuthFlowFn: func(_ context.Context, state string) (domain.FlowState, error) {
			if tc.known && state == "good" {
				return domain.FlowState{RedirectTo: "http://app.local"}, nil
			}
			return domain.FlowState{}, domain.ErrNotFound
		}})
		r := gin.New()
		r.GET("/cb", h.handleOAuthCallback("unitfake"))
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "https://www.schedule-match.com/cb?state="+tc.state+"&code=c", nil)
		r.ServeHTTP(w, req)
		if w.Code != tc.wantStatus {
			t.Fatalf("%s: status %d want %d body %s", tc.name, w.Code, tc.wantStatus, w.Body.String())
		}
	}
}

// The post-commit revoke must survive a client disconnect.
func TestUpdateUser_PasswordChangeRevokeSurvivesCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)
	ctx, cancel := context.WithCancel(context.Background())
	revokeCalled := false
	var revokeCtxErr error
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{}}, jwtKeys: km, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		authSvc: &stubAuthService{
			updateUserFn: func(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error) {
				cancel() // client disconnects right after the update commits
				return testUserRow(id), nil
			},
			revokeOtherSessionsFn: func(ctx context.Context, uid, keep string) error {
				revokeCalled = true
				revokeCtxErr = ctx.Err()
				return nil
			},
		}}
	r := gin.New()
	r.PUT("/auth/v1/user", jwtAuth(km, true), h.handleUpdateUser)
	claims := jwt.MapClaims{"sub": "u1", "role": "authenticated", "aud": "authenticated", "session_id": "sess-1", "exp": time.Now().Add(time.Hour).Unix()}
	req := httptest.NewRequest("PUT", "/auth/v1/user", strings.NewReader(`{"password":"new-long-password"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+signToken(t, km, claims))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !revokeCalled {
		t.Fatal("revoke not called")
	}
	if revokeCtxErr != nil {
		t.Fatalf("revoke ctx must survive client disconnect, got err=%v", revokeCtxErr)
	}
}

// The post-verify aal1 revoke must survive a client disconnect.
func TestMFAVerify_RevokeBelowAAL2SurvivesCancel(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	factorID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	uid := "11111111-2222-3333-4444-555555555555"

	reqCtx, cancel := context.WithCancel(context.Background())
	revokeCalled := false
	var revokeCtxErr error
	svc := &stubAuthService{
		getFactorForVerifyFn: func(ctx context.Context, fID, userID string) (domain.MFAFactor, error) {
			return domain.MFAFactor{Secret: secret, Status: "unverified"}, nil
		},
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"id": uid, "email": "u@e.com", "email_verified": true,
				"raw_app_meta_data": `{}`, "raw_user_meta_data": `{}`,
				"created_at": time.Now(), "updated_at": time.Now(),
			}, nil
		},
		revokeBelowAAL2Fn: func(ctx context.Context, userID, sessionID string, allSessions bool) error {
			cancel() // request context is canceled the moment the handler would return
			revokeCalled = true
			revokeCtxErr = ctx.Err()
			return nil
		},
	}
	m := newMFAHarness(t, svc)

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	req := httptest.NewRequest("POST", "/auth/v1/factors/"+factorID+"/verify",
		strings.NewReader(`{"challenge_id":"c1","code":"`+code+`"}`)).WithContext(reqCtx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("apikey", "inz_publishable_mfatest")
	w := httptest.NewRecorder()
	m.r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !revokeCalled {
		t.Fatal("revoke not called")
	}
	if revokeCtxErr != nil {
		t.Fatalf("revoke ctx must survive client disconnect, got err=%v", revokeCtxErr)
	}
}

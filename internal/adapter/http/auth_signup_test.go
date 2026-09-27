package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/instancez/instancez/internal/domain"
)

func signupRouter(t *testing.T, svc *stubAuthService, verifyEmail bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{JWTExpiry: "15m", Email: &domain.AuthEmail{VerifyEmail: verifyEmail}}},
		authSvc: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t)}
	r := gin.New()
	r.POST("/signup", h.handleSignupDispatch)
	return r
}

const signupBody = `{"email":"new@e.com","password":"long-enough-pw"}`

func TestSignup_VerifyEmailWithholdsSession(t *testing.T) {
	for _, verify := range []bool{true, false} {
		inserted := false
		svc := &stubAuthService{
			createUserFn: func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) { return testUserRow("real-id"), nil },
			insertRefreshTokenFn: func(context.Context, string, string, domain.SessionMeta, int64) error {
				inserted = true
				return nil
			},
		}
		w := postJSON(signupRouter(t, svc, verify), "/signup", signupBody)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		hasSession := body["access_token"] != nil
		if w.Code != 200 || hasSession == verify || inserted == verify {
			t.Errorf("verify=%v: status %d session=%v inserted=%v", verify, w.Code, hasSession, inserted)
		}
		if verify && body["id"] != "real-id" {
			t.Errorf("verify=true must return the user object, got %v", body)
		}
	}
}

type stubEmailSender struct{ sent int }

func (s *stubEmailSender) Send(context.Context, domain.EmailMessage) error {
	s.sent++
	return nil
}

func TestSignup_VerifyEmailSendsVerificationEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubAuthService{
		createUserFn: func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) { return testUserRow("real-id"), nil },
	}
	sender := &stubEmailSender{}
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{JWTExpiry: "15m", Email: &domain.AuthEmail{VerifyEmail: true}}},
		authSvc: svc, email: sender, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t)}
	r := gin.New()
	r.POST("/signup", h.handleSignupDispatch)

	w := postJSON(r, "/signup", signupBody)
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if sender.sent != 1 {
		t.Errorf("verification email sent %d times, want 1", sender.sent)
	}
}

func TestUpdateUser_PasswordChangeSignsOutOtherSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name, body, sid string
		status          int
		wantOthers      string
		wantAll         bool
	}{
		{"password with session", `{"password":"new-long-password"}`, "sess-1", 200, "sess-1", false},
		{"password legacy token", `{"password":"new-long-password"}`, "", 200, "", true},
		{"metadata only", `{"data":{"a":1}}`, "sess-1", 200, "", false},
		{"too short", `{"password":"short"}`, "sess-1", 400, "", false},
		{"empty password", `{"password":""}`, "sess-1", 200, "", false},
	}
	for _, tc := range cases {
		others, all := "", false
		km := stubKeys(t)
		h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{}}, jwtKeys: km, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			authSvc: &stubAuthService{
				updateUserFn:            func(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error) { return testUserRow(id), nil },
				revokeOtherSessionsFn:   func(ctx context.Context, uid, keep string) error { others = keep; return nil },
				revokeAllUserSessionsFn: func(context.Context, string) error { all = true; return nil },
			}}
		r := gin.New()
		r.PUT("/auth/v1/user", jwtAuth(km, true), h.handleUpdateUser)
		claims := jwt.MapClaims{"sub": "u1", "role": "authenticated", "aud": "authenticated", "exp": time.Now().Add(time.Hour).Unix()}
		if tc.sid != "" {
			claims["session_id"] = tc.sid
		}
		req := httptest.NewRequest("PUT", "/auth/v1/user", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+signToken(t, km, claims))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.status || others != tc.wantOthers || all != tc.wantAll {
			t.Errorf("%s: status %d others=%q all=%v", tc.name, w.Code, others, all)
		}
	}
}

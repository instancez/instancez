package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/instancez/instancez/internal/domain"
)

func otpRouter(t *testing.T, svc *stubAuthService, allowSignup bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{AllowSignup: &allowSignup, Email: &domain.AuthEmail{}}},
		authSvc: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t)}
	r := gin.New()
	r.POST("/otp", h.handleOTP)
	r.POST("/resend", h.handleResend)
	r.POST("/recover", h.handleRecover)
	r.GET("/verify", h.handleVerifyGET)
	return r
}

func TestOTP_SignupsDisabledNeverCreatesUser(t *testing.T) {
	for _, tc := range []struct {
		name   string
		allow  bool
		body   string
		create bool
	}{
		{"signups off", false, `{"email":"new@e.com"}`, false},
		{"signups off, create_user true", false, `{"email":"new@e.com","create_user":true}`, false},
		{"create_user false", true, `{"email":"new@e.com","create_user":false}`, false},
		{"signups on", true, `{"email":"new@e.com"}`, true},
	} {
		created, issued := false, false
		svc := &stubAuthService{
			createUserFn: func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
				created = true
				return map[string]any{"id": "u-new"}, nil
			},
			createOTPCodeFn: func(context.Context, string, string, string, string, string, int64) error { issued = true; return nil },
		}
		w := postJSON(otpRouter(t, svc, tc.allow), "/otp", tc.body)
		if w.Code != 200 || created != tc.create || issued != tc.create {
			t.Errorf("%s: status %d created=%v issued=%v", tc.name, w.Code, created, issued)
		}
	}
}

func TestOTP_SignupsDisabledStillServesKnownUser(t *testing.T) {
	issued := false
	svc := &stubAuthService{
		getUserIDByEmailFn: func(context.Context, string) (string, error) { return "u1", nil },
		createOTPCodeFn:    func(context.Context, string, string, string, string, string, int64) error { issued = true; return nil },
	}
	if w := postJSON(otpRouter(t, svc, false), "/otp", `{"email":"a@e.com"}`); w.Code != 200 || !issued {
		t.Fatalf("status %d issued=%v", w.Code, issued)
	}
}

func TestOTP_ResendCooldown(t *testing.T) {
	known := func(context.Context, string) (string, error) { return "u1", nil }
	for _, tc := range []struct {
		path, body, purpose string
		hitStatus           int
	}{
		// /otp and /recover stay silent so a cooldown hit can't reveal the account.
		{"/otp", `{"email":"a@e.com"}`, "magiclink", 200},
		{"/resend", `{"type":"signup","email":"a@e.com"}`, "signup", 429},
		{"/resend", `{"type":"magiclink","email":"a@e.com"}`, "magiclink", 429},
		{"/recover", `{"email":"a@e.com"}`, "recovery", 200},
	} {
		for _, recent := range []bool{true, false} {
			var gotUID, gotPurpose string
			var gotWithin time.Duration
			cleared, issued := false, false
			svc := &stubAuthService{
				getUserIDByEmailFn: known,
				recentOTPSentFn: func(ctx context.Context, uid, purpose string, within time.Duration) (bool, error) {
					gotUID, gotPurpose, gotWithin = uid, purpose, within
					return recent, nil
				},
				deleteUserTokensByPurposeFn: func(context.Context, string, string) error { cleared = true; return nil },
				createOTPCodeFn:             func(context.Context, string, string, string, string, string, int64) error { issued = true; return nil },
				createOneTimeTokenFn:        func(context.Context, string, string, string, int64) error { issued = true; return nil },
			}
			w := postJSON(otpRouter(t, svc, true), tc.path, tc.body)
			if gotUID != "u1" || gotPurpose != tc.purpose || gotWithin != emailResendCooldown {
				t.Errorf("%s: cooldown checked for %q/%q/%v", tc.path, gotUID, gotPurpose, gotWithin)
			}
			if recent {
				if w.Code != tc.hitStatus || issued || cleared {
					t.Errorf("%s recent: status %d issued=%v cleared=%v", tc.path, w.Code, issued, cleared)
				}
				if tc.hitStatus == 429 && !strings.Contains(w.Body.String(), fmt.Sprintf("after %d seconds", int(emailResendCooldown.Seconds()))) {
					t.Errorf("%s recent: message must name the cooldown: %s", tc.path, w.Body.String())
				}
				if rl := strings.Contains(w.Body.String(), "over_email_send_rate_limit"); rl != (tc.hitStatus == 429) {
					t.Errorf("%s recent: body %s", tc.path, w.Body.String())
				}
				if tc.hitStatus == 200 && strings.TrimSpace(w.Body.String()) != "{}" {
					t.Errorf("%s recent: silent hit must match the unknown-email body, got %s", tc.path, w.Body.String())
				}
			}
			if !recent && (w.Code != 200 || !issued) {
				t.Errorf("%s fresh: status %d issued=%v", tc.path, w.Code, issued)
			}
		}
	}
}

func TestOTP_CooldownCheckErrorFailsOpen(t *testing.T) {
	issued := false
	svc := &stubAuthService{
		getUserIDByEmailFn: func(context.Context, string) (string, error) { return "u1", nil },
		recentOTPSentFn: func(context.Context, string, string, time.Duration) (bool, error) {
			return true, errors.New("db down")
		},
		createOTPCodeFn: func(context.Context, string, string, string, string, string, int64) error { issued = true; return nil },
	}
	if w := postJSON(otpRouter(t, svc, true), "/resend", `{"type":"signup","email":"a@e.com"}`); w.Code != 200 || !issued {
		t.Fatalf("status %d issued=%v", w.Code, issued)
	}
}

func TestOTP_UnknownEmailSkipsCooldownAndStays200(t *testing.T) {
	checked := false
	svc := &stubAuthService{recentOTPSentFn: func(context.Context, string, string, time.Duration) (bool, error) { checked = true; return true, nil }}
	for _, p := range []string{"/resend", "/recover"} {
		body := `{"email":"ghost@e.com"}`
		if p == "/resend" {
			body = `{"type":"signup","email":"ghost@e.com"}`
		}
		if w := postJSON(otpRouter(t, svc, true), p, body); w.Code != 200 || checked || strings.TrimSpace(w.Body.String()) != "{}" {
			t.Errorf("%s: status %d checked=%v body=%s", p, w.Code, checked, w.Body.String())
		}
	}
}

func TestVerifyGET_SecondClickLoses(t *testing.T) {
	for _, purpose := range []string{"signup", "recovery"} {
		verified, sessioned := false, false
		svc := &stubAuthService{
			peekOneTimeTokenFn: func(context.Context, string) (domain.OTPRow, error) {
				return domain.OTPRow{UserID: "u1", Purpose: purpose}, nil
			},
			deleteOneTimeTokenFn: func(context.Context, string) error { return domain.ErrInvalidToken },
			markEmailVerifiedFn:  func(context.Context, string) { verified = true },
			getUserByIDFn: func(context.Context, string) (map[string]any, error) {
				sessioned = true
				return map[string]any{"id": "u1"}, nil
			},
		}
		w := httptest.NewRecorder()
		otpRouter(t, svc, true).ServeHTTP(w, httptest.NewRequest("GET", "/verify?token=t", nil))
		if w.Code != 400 || verified || sessioned {
			t.Errorf("%s: status %d verified=%v sessioned=%v", purpose, w.Code, verified, sessioned)
		}
	}
}

func TestResend_NothingToResendIsSilent(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		verified   any
		send       bool
	}{
		{"signup, confirmed", `{"type":"signup","email":"a@e.com"}`, true, false},
		{"signup, unconfirmed", `{"type":"signup","email":"a@e.com"}`, false, true},
		{"signup, flag missing", `{"type":"signup","email":"a@e.com"}`, nil, true},
		{"email_change, none pending", `{"type":"email_change","email":"a@e.com"}`, false, false},
		{"magiclink, confirmed", `{"type":"magiclink","email":"a@e.com"}`, true, true},
	} {
		issued, cleared, checked := false, false, false
		svc := &stubAuthService{
			getUserIDByEmailFn: func(context.Context, string) (string, error) { return "u1", nil },
			getUserByIDFn: func(context.Context, string) (map[string]any, error) {
				return map[string]any{"id": "u1", "email_verified": tc.verified}, nil
			},
			recentOTPSentFn: func(context.Context, string, string, time.Duration) (bool, error) {
				checked = true
				return false, nil
			},
			deleteUserTokensByPurposeFn: func(context.Context, string, string) error { cleared = true; return nil },
			createOTPCodeFn:             func(context.Context, string, string, string, string, string, int64) error { issued = true; return nil },
		}
		w := postJSON(otpRouter(t, svc, true), "/resend", tc.body)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" || issued != tc.send || cleared != tc.send || checked != tc.send {
			t.Errorf("%s: status %d body %s issued=%v cleared=%v checked=%v", tc.name, w.Code, w.Body.String(), issued, cleared, checked)
		}
	}
}

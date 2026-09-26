package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/instancez/instancez/internal/domain"
)

func testUserRow(id string) map[string]any {
	return map[string]any{
		"id": id, "email": "u@e.com", "email_verified": true,
		"raw_app_meta_data": `{}`, "raw_user_meta_data": `{}`,
		"created_at": time.Now(), "updated_at": time.Now(),
	}
}

func newTokenRouter(t *testing.T, svc *stubAuthService, auth *domain.Auth) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if auth == nil {
		auth = &domain.Auth{JWTExpiry: "15m"}
	}
	h := &AuthHandler{cfg: &domain.Config{Auth: auth}, authSvc: svc,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t)}
	r := gin.New()
	r.POST("/auth/v1/token", h.handleToken)
	return r
}

func postJSON(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func accessClaims(t *testing.T, w *httptest.ResponseRecorder) jwt.MapClaims {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v (%s)", err, w.Body.String())
	}
	tok, _ := body["access_token"].(string)
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse access_token: %v (%s)", err, w.Body.String())
	}
	return parsed.Claims.(jwt.MapClaims)
}

func TestPasswordGrant_JWTCarriesSupabaseSessionClaims(t *testing.T) {
	var saved domain.SessionMeta
	svc := &stubAuthService{
		verifyPasswordFn: func(ctx context.Context, email, pw string) (map[string]any, error) { return testUserRow("u1"), nil },
		insertRefreshTokenFn: func(ctx context.Context, uid, tok string, m domain.SessionMeta, exp int64) error {
			saved = m
			return nil
		},
	}
	w := postJSON(newTokenRouter(t, svc, nil), "/auth/v1/token?grant_type=password", `{"email":"u@e.com","password":"pw"}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	c := accessClaims(t, w)
	if c["aal"] != "aal1" {
		t.Errorf("aal = %v, want top-level aal1", c["aal"])
	}
	amr, _ := c["amr"].([]any)
	first, _ := amr[0].(map[string]any)
	if len(amr) != 1 || first["method"] != "password" || first["timestamp"] == nil {
		t.Errorf("amr = %v", c["amr"])
	}
	if sid, _ := c["session_id"].(string); sid != saved.SessionID {
		t.Errorf("session_id %q must match the persisted refresh row %q", sid, saved.SessionID)
	} else if _, err := uuid.Parse(sid); err != nil {
		t.Errorf("session_id %q must be a uuid: %v", sid, err)
	}
	if am, _ := c["app_metadata"].(map[string]any); am["aal"] != nil {
		t.Errorf("aal must not leak into app_metadata: %v", am)
	}
}

func TestRefreshGrant_PreservesSessionState(t *testing.T) {
	var next domain.RefreshRotation
	inserted := false
	in := domain.SessionMeta{SessionID: "sess-1", AAL: "aal2",
		AMR: []domain.AMREntry{{Method: "totp", Timestamp: 200}, {Method: "password", Timestamp: 100}}}
	svc := &stubAuthService{
		consumeRefreshFn: func(ctx context.Context, tok string, n domain.RefreshRotation) (map[string]any, domain.SessionMeta, string, error) {
			next = n
			return testUserRow("u1"), in, "existing-child", nil
		},
		insertRefreshTokenFn: func(ctx context.Context, uid, tok string, m domain.SessionMeta, exp int64) error {
			inserted = true
			return nil
		},
	}
	w := postJSON(newTokenRouter(t, svc, nil), "/auth/v1/token?grant_type=refresh_token", `{"refresh_token":"rt"}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	c := accessClaims(t, w)
	if c["aal"] != "aal2" || c["session_id"] != "sess-1" {
		t.Errorf("claims aal=%v sid=%v", c["aal"], c["session_id"])
	}
	amr, _ := c["amr"].([]any)
	if len(amr) != 2 || amr[0].(map[string]any)["method"] != "totp" {
		t.Errorf("amr order lost: %v", c["amr"])
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["refresh_token"] != "existing-child" {
		t.Errorf("refresh_token = %v, want the token the service handed out", body["refresh_token"])
	}
	if inserted {
		t.Error("refresh grant must not mint a second refresh row")
	}
	if next.Token == "" || next.ExpiresAt <= time.Now().Unix() || next.IP == "" {
		t.Errorf("rotation request incomplete: %+v", next)
	}
}

func TestRefreshGrant_ErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{domain.ErrRefreshReuse, "reuse"},
		{domain.ErrRefreshExpired, "expired"},
		{domain.ErrUnauthorized, "Invalid refresh token"},
	} {
		svc := &stubAuthService{consumeRefreshFn: func(ctx context.Context, tok string, n domain.RefreshRotation) (map[string]any, domain.SessionMeta, string, error) {
			return nil, domain.SessionMeta{}, "", tc.err
		}}
		w := postJSON(newTokenRouter(t, svc, nil), "/auth/v1/token?grant_type=refresh_token", `{"refresh_token":"rt"}`)
		if w.Code != 401 || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%v: status %d body %s", tc.err, w.Code, w.Body.String())
		}
	}
}

func TestJWTClaims_Garbage(t *testing.T) {
	for _, raw := range []string{"", "a.b.c", "not-a-jwt"} {
		if c := jwtClaims(raw); c == nil || len(c) != 0 {
			t.Errorf("%q: want empty non-nil claims, got %v", raw, c)
		}
	}
}

func TestMFAVerify_KeepsSessionIDAndPrependsTOTP(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	var saved domain.SessionMeta
	svc := &stubAuthService{
		getFactorForVerifyFn: func(ctx context.Context, fID, userID string) (domain.MFAFactor, error) {
			return domain.MFAFactor{Secret: secret, Status: "verified"}, nil
		},
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) { return testUserRow(id), nil },
		insertRefreshTokenFn: func(ctx context.Context, uid, tok string, m domain.SessionMeta, exp int64) error {
			saved = m
			return nil
		},
	}
	m := newMFAHarness(t, svc)
	m.token = signToken(t, m.h.jwtKeys, jwt.MapClaims{
		"sub": m.userID, "role": "authenticated", "aud": "authenticated",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		"session_id": "sess-mfa", "aal": "aal1",
		"amr": []map[string]any{{"method": "totp", "timestamp": 150}, {"method": "password", "timestamp": 100}},
	})
	code, _ := totp.GenerateCode(secret, time.Now())
	w := m.do("POST", "/auth/v1/factors/f1/verify", `{"code":"`+code+`"}`)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	c := accessClaims(t, w)
	if c["session_id"] != "sess-mfa" || c["aal"] != "aal2" {
		t.Errorf("sid=%v aal=%v, want sess-mfa/aal2", c["session_id"], c["aal"])
	}
	amr, _ := c["amr"].([]any)
	if len(amr) != 2 || amr[0].(map[string]any)["method"] != "totp" || amr[1].(map[string]any)["method"] != "password" {
		t.Errorf("amr = %v, want [totp, password]", c["amr"])
	}
	if saved.SessionID != "sess-mfa" || saved.AAL != "aal2" || len(saved.AMR) != 2 {
		t.Errorf("refresh row = %+v", saved)
	}
}

func TestVerify_AMRMethodPerType(t *testing.T) {
	for typ, want := range map[string]string{
		"signup": "email/signup", "email": "otp", "magiclink": "magiclink",
		"recovery": "recovery", "email_change": "email_change",
	} {
		svc := &stubAuthService{
			verifyOTPFn: func(ctx context.Context, tok, email string, p []string) (domain.OTPRow, error) {
				return domain.OTPRow{UserID: "u1", Purpose: typ}, nil
			},
			getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) { return testUserRow(id), nil },
		}
		r := gin.New()
		h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{JWTExpiry: "15m"}}, authSvc: svc,
			logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t)}
		r.POST("/auth/v1/verify", h.handleVerify)
		w := postJSON(r, "/auth/v1/verify", `{"type":"`+typ+`","token":"t"}`)
		if w.Code != 200 {
			t.Fatalf("%s: status %d: %s", typ, w.Code, w.Body.String())
		}
		amr, _ := accessClaims(t, w)["amr"].([]any)
		if len(amr) != 1 || amr[0].(map[string]any)["method"] != want {
			t.Errorf("%s: amr = %v, want method %s", typ, amr, want)
		}
	}
}

func TestAddAMR(t *testing.T) {
	e := func(m string, ts int64) domain.AMREntry { return domain.AMREntry{Method: m, Timestamp: ts} }
	for name, tc := range map[string]struct {
		in   []domain.AMREntry
		add  domain.AMREntry
		want []domain.AMREntry
	}{
		"nil":              {nil, e("totp", 5), []domain.AMREntry{e("totp", 5)}},
		"re-verify":        {[]domain.AMREntry{e("totp", 3), e("password", 1)}, e("totp", 5), []domain.AMREntry{e("totp", 5), e("password", 1)}},
		"same second":      {[]domain.AMREntry{e("totp", 5)}, e("totp", 5), []domain.AMREntry{e("totp", 5)}},
		"legacy dupes":     {[]domain.AMREntry{e("password", 1), e("password", 4)}, e("totp", 5), []domain.AMREntry{e("totp", 5), e("password", 4)}},
		"unsorted history": {[]domain.AMREntry{e("otp", 1), e("password", 2)}, e("totp", 3), []domain.AMREntry{e("totp", 3), e("password", 2), e("otp", 1)}},
	} {
		if got := addAMR(tc.in, tc.add); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

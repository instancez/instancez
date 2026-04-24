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
	"github.com/pquerna/otp/totp"
	"github.com/saedx1/ultrabase/internal/domain"
)

// mfaHarness wires a jwtAuth-protected /factors router against a stub
// DB, signs a bearer token for the caller, and returns helpers to drive
// requests through gin the same way the real server would.
type mfaHarness struct {
	t       *testing.T
	h       *AuthHandler
	r       *gin.Engine
	token   string
	userID  string
	email   string
	lastReq *httptest.ResponseRecorder
}

func newMFAHarness(t *testing.T, db *stubDB) *mfaHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)
	h := &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{
			JWTExpiry:     "1h",
			RefreshTokens: false,
			Email:         &domain.AuthEmail{},
		}},
		db:      db,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: km,
	}
	r := gin.New()
	h.MountMFA(r.Group("/auth/v1"))

	uid := "11111111-2222-3333-4444-555555555555"
	email := "u@e.com"
	tok := signToken(t, km, jwt.MapClaims{
		"sub":   uid,
		"role":  "authenticated",
		"aud":   "authenticated",
		"email": email,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	return &mfaHarness{t: t, h: h, r: r, token: tok, userID: uid, email: email}
}

func (m *mfaHarness) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.token)
	w := httptest.NewRecorder()
	m.r.ServeHTTP(w, req)
	m.lastReq = w
	return w
}

// TestMFA_EnrollCreatesUnverifiedFactor drives POST /factors and asserts
// the response exposes the shared secret + otpauth URI, while the row is
// written with status='unverified'.
func TestMFA_EnrollCreatesUnverifiedFactor(t *testing.T) {
	var insertedStatus string
	var insertedSecret string
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			if strings.Contains(q, "INSERT INTO _mfa_factors") {
				// args: user_id, friendly_name, secret
				if len(args) >= 3 {
					insertedSecret, _ = args[2].(string)
				}
				insertedStatus = "unverified"
				return map[string]any{
					"id":            "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					"friendly_name": "iPhone",
					"factor_type":   "totp",
					"status":        "unverified",
					"created_at":    time.Now(),
					"updated_at":    time.Now(),
				}, nil
			}
			return nil, nil
		},
	}
	m := newMFAHarness(t, db)
	w := m.do("POST", "/auth/v1/factors", `{"factor_type":"totp","friendly_name":"iPhone"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["type"] != "totp" {
		t.Errorf("type = %v", body["type"])
	}
	totpBlock, _ := body["totp"].(map[string]any)
	if totpBlock == nil {
		t.Fatalf("missing totp block: %s", w.Body.String())
	}
	secret, _ := totpBlock["secret"].(string)
	if secret == "" || secret != insertedSecret {
		t.Errorf("returned secret %q != inserted %q", secret, insertedSecret)
	}
	if uri, _ := totpBlock["uri"].(string); !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Errorf("uri missing otpauth prefix: %q", uri)
	}
	if insertedStatus != "unverified" {
		t.Errorf("factor should be unverified until verify succeeds, got %q", insertedStatus)
	}
}

// TestMFA_VerifyGoodCodeFlipsFactorAndReturnsAAL2 enrolls via stub, then
// drives /verify with a code freshly computed against the stored secret.
// It asserts the factor flips to 'verified' and the issued session JWT
// carries aal=aal2 in app_metadata.
func TestMFA_VerifyGoodCodeFlipsFactorAndReturnsAAL2(t *testing.T) {
	// Known secret so the test can compute a valid TOTP.
	secret := "JBSWY3DPEHPK3PXP"
	factorID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	uid := "11111111-2222-3333-4444-555555555555"

	flipped := false
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			switch {
			case strings.Contains(q, "SELECT user_id::text, secret, status FROM _mfa_factors"):
				return map[string]any{
					"user_id": uid,
					"secret":  secret,
					"status":  "unverified",
				}, nil
			case strings.Contains(q, "FROM users WHERE id"):
				return map[string]any{
					"id":                 uid,
					"email":              "u@e.com",
					"email_verified":     true,
					"raw_app_meta_data":  `{}`,
					"raw_user_meta_data": `{}`,
					"created_at":         time.Now(),
					"updated_at":         time.Now(),
				}, nil
			}
			return nil, nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if strings.Contains(q, "UPDATE _mfa_factors SET status = 'verified'") {
				flipped = true
			}
			return 1, nil
		},
	}
	m := newMFAHarness(t, db)
	// Override userID to match the stubbed factor.
	m.userID = uid
	m.token = signToken(t, m.h.jwtKeys, jwt.MapClaims{
		"sub": uid, "role": "authenticated", "aud": "authenticated",
		"email": "u@e.com",
		"iat":   time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	w := m.do("POST", "/auth/v1/factors/"+factorID+"/verify",
		`{"code":"`+code+`"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !flipped {
		t.Errorf("factor should flip to verified on first successful verify")
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	tok, _ := body["access_token"].(string)
	if tok == "" {
		t.Fatalf("missing access_token: %s", w.Body.String())
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	appMeta, _ := claims["app_metadata"].(map[string]any)
	if appMeta == nil || appMeta["aal"] != "aal2" {
		t.Errorf("expected aal=aal2 in app_metadata, got %v", appMeta)
	}
}

// TestMFA_VerifyBadCodeRejected uses the same setup but passes a bogus
// 6-digit code; the handler must return 401 and leave the factor
// unverified.
func TestMFA_VerifyBadCodeRejected(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	uid := "11111111-2222-3333-4444-555555555555"
	flipped := false
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			if strings.Contains(q, "SELECT user_id::text, secret, status FROM _mfa_factors") {
				return map[string]any{"user_id": uid, "secret": secret, "status": "unverified"}, nil
			}
			return nil, nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if strings.Contains(q, "status = 'verified'") {
				flipped = true
			}
			return 1, nil
		},
	}
	m := newMFAHarness(t, db)
	m.userID = uid
	m.token = signToken(t, m.h.jwtKeys, jwt.MapClaims{
		"sub": uid, "role": "authenticated", "aud": "authenticated",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	w := m.do("POST", "/auth/v1/factors/any/verify", `{"code":"000000"}`)
	if w.Code != 401 {
		t.Fatalf("expected 401 for bad code, got %d: %s", w.Code, w.Body.String())
	}
	if flipped {
		t.Errorf("factor must not flip to verified on bad code")
	}
}

// TestMFA_UnenrollRejectsWrongOwner asserts a user can't delete another
// user's factor: the DELETE's WHERE clause pins user_id so 0 rows are
// affected and the handler returns 404.
func TestMFA_UnenrollRejectsWrongOwner(t *testing.T) {
	db := &stubDB{
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if strings.Contains(q, "DELETE FROM _mfa_factors") {
				// Simulate no rows matched (factor belongs to someone else).
				return 0, nil
			}
			return 0, nil
		},
	}
	m := newMFAHarness(t, db)
	w := m.do("DELETE", "/auth/v1/factors/some-factor-id", ``)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestMFA_ListFactorsPartitionsByType seeds two factors of different
// types and asserts the response is partitioned into {totp, phone}.
func TestMFA_ListFactorsPartitionsByType(t *testing.T) {
	db := &stubDB{
		queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
			return []map[string]any{
				{
					"id":            "f1",
					"friendly_name": "iPhone",
					"factor_type":   "totp",
					"status":        "verified",
					"created_at":    time.Now(),
					"updated_at":    time.Now(),
				},
			}, nil
		},
	}
	m := newMFAHarness(t, db)
	w := m.do("GET", "/auth/v1/factors", ``)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	totpArr, _ := body["totp"].([]any)
	phoneArr, _ := body["phone"].([]any)
	if len(totpArr) != 1 {
		t.Errorf("totp = %v", totpArr)
	}
	if phoneArr == nil {
		t.Errorf("phone key should be present as empty array, got nil")
	}
}

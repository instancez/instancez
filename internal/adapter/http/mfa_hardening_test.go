package http

import (
	"context"
	"encoding/json"
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

	"github.com/instancez/instancez/internal/domain"
)

const mfaSecret = "JBSWY3DPEHPK3PXP"

func TestMatchTOTPStep(t *testing.T) {
	now := time.Unix(1_700_000_015, 0)
	for _, off := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
		code, _ := totp.GenerateCode(mfaSecret, now.Add(off))
		step, ok := matchTOTPStep(code, mfaSecret, now)
		if !ok || step != now.Add(off).Unix()/30 {
			t.Errorf("offset %v: step=%d ok=%v", off, step, ok)
		}
	}
	far, _ := totp.GenerateCode(mfaSecret, now.Add(90*time.Second))
	for _, bad := range []string{far, "", "abcdef", "1234567", "12345", "１２３４５６"} {
		if _, ok := matchTOTPStep(bad, mfaSecret, now); ok {
			t.Errorf("%q must not match", bad)
		}
	}
	code, _ := totp.GenerateCode(mfaSecret, now)
	if _, ok := matchTOTPStep(code, "", now); ok {
		t.Error("empty secret must not match")
	}
}

func goodVerifySvc(status string) *stubAuthService {
	return &stubAuthService{
		getFactorForVerifyFn: func(context.Context, string, string) (domain.MFAFactor, error) {
			return domain.MFAFactor{Secret: mfaSecret, Status: status}, nil
		},
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) { return testUserRow(id), nil },
	}
}

func verifyBody() string {
	code, _ := totp.GenerateCode(mfaSecret, time.Now())
	return `{"challenge_id":"c1","code":"` + code + `"}`
}

func TestMFA_VerifyRequiresChallengeID(t *testing.T) {
	m := newMFAHarness(t, goodVerifySvc("verified"))
	code, _ := totp.GenerateCode(mfaSecret, time.Now())
	for _, body := range []string{`{"code":"` + code + `"}`, `{"challenge_id":"","code":"` + code + `"}`, `{"challenge_id":null,"code":"` + code + `"}`} {
		if w := m.do("POST", "/auth/v1/factors/f1/verify", body); w.Code != 400 {
			t.Errorf("%s: status %d, want 400", body, w.Code)
		}
	}
}

func TestMFA_VerifyReplayedStepRejected(t *testing.T) {
	svc := goodVerifySvc("verified")
	marked := false
	svc.consumeTOTPStepFn = func(context.Context, string, int64) (bool, error) { return false, nil }
	svc.markChallengeVerifiedFn = func(context.Context, string) error { marked = true; return nil }
	w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/verify", verifyBody())
	if w.Code != 401 || marked {
		t.Fatalf("replay: status %d marked=%v", w.Code, marked)
	}
	svc.consumeTOTPStepFn = func(context.Context, string, int64) (bool, error) { return false, errors.New("db down") }
	if w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/verify", verifyBody()); w.Code != 500 || marked {
		t.Fatalf("consume error: status %d marked=%v", w.Code, marked)
	}
}

func TestMFA_VerifyLosingChallengeRace(t *testing.T) {
	svc := goodVerifySvc("verified")
	svc.markChallengeVerifiedFn = func(context.Context, string) error { return domain.ErrChallengeUsed }
	w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/verify", verifyBody())
	if w.Code != 400 || strings.Contains(w.Body.String(), "access_token") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestMFA_VerifyChecksChallengeBeforeCode(t *testing.T) {
	calls, consumed := 0, false
	svc := goodVerifySvc("verified")
	svc.validateChallengeFn = func(context.Context, string, string) error { calls++; return nil }
	svc.consumeTOTPStepFn = func(context.Context, string, int64) (bool, error) { consumed = true; return true, nil }
	w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/verify", `{"challenge_id":"c1","code":"000000"}`)
	if w.Code != 401 || calls != 1 || consumed {
		t.Fatalf("bad code: status %d validate calls %d consumed %v", w.Code, calls, consumed)
	}
	svc.validateChallengeFn = func(context.Context, string, string) error { return domain.ErrChallengeExpired }
	if w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/verify", verifyBody()); w.Code != 401 || consumed {
		t.Fatalf("expired challenge: status %d consumed %v", w.Code, consumed)
	}
}

func TestMFA_VerifyPendingFactorNeedsAAL2WhenAnotherIsVerified(t *testing.T) {
	cases := []struct {
		name     string
		statuses map[string]string
		aal      string
		want     int
	}{
		{"sibling verified, aal1", map[string]string{"f1": "unverified", "f0": "verified"}, "aal1", 403},
		{"sibling verified, aal2", map[string]string{"f1": "unverified", "f0": "verified"}, "aal2", 200},
		{"first factor, aal1", map[string]string{"f1": "unverified"}, "aal1", 200},
	}
	for _, tc := range cases {
		validated := false
		svc := goodVerifySvc("unverified")
		svc.listFactorsFn = factorsSvc(tc.statuses, nil).listFactorsFn
		svc.validateChallengeFn = func(context.Context, string, string) error { validated = true; return nil }
		w := newMFAHarness(t, svc).as(tc.aal, "s").do("POST", "/auth/v1/factors/f1/verify", verifyBody())
		if w.Code != tc.want {
			t.Errorf("%s: status %d want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
		if tc.want == 403 && validated {
			t.Errorf("%s: gate must run before spending a challenge attempt", tc.name)
		}
	}
}

func TestMFA_VerifyRevokesAAL1TokensAfterIssuingAAL2(t *testing.T) {
	for status, wantAll := range map[string]bool{"unverified": true, "verified": false} {
		inserted, called, all := false, false, false
		sid := ""
		svc := goodVerifySvc(status)
		svc.insertRefreshTokenFn = func(context.Context, string, string, domain.SessionMeta, int64) error {
			inserted = true
			return nil
		}
		svc.revokeBelowAAL2Fn = func(ctx context.Context, uid, s string, a bool) error {
			if !inserted {
				t.Errorf("%s: aal1 tokens revoked before the aal2 row exists", status)
			}
			called, sid, all = true, s, a
			return nil
		}
		w := newMFAHarness(t, svc).as("aal1", "sess-9").do("POST", "/auth/v1/factors/f1/verify", verifyBody())
		if w.Code != 200 || !called || sid != "sess-9" || all != wantAll {
			t.Errorf("%s: status %d called=%v sid=%q all=%v", status, w.Code, called, sid, all)
		}
	}
}

func TestMFA_VerifyFailureRevokesNothing(t *testing.T) {
	svc := goodVerifySvc("unverified")
	svc.revokeBelowAAL2Fn = func(context.Context, string, string, bool) error {
		t.Error("revoked on a failed verify")
		return nil
	}
	newMFAHarness(t, svc).as("aal1", "s").do("POST", "/auth/v1/factors/f1/verify", `{"challenge_id":"c1","code":"000000"}`)
}

func factorsSvc(statuses map[string]string, listErr error) *stubAuthService {
	return &stubAuthService{
		listFactorsFn: func(context.Context, string) ([]map[string]any, error) {
			rows := []map[string]any{}
			for id, st := range statuses {
				rows = append(rows, map[string]any{"id": id, "status": st, "factor_type": "totp"})
			}
			return rows, listErr
		},
	}
}

func TestMFA_EnrollRequiresAAL2WhenVerifiedFactorExists(t *testing.T) {
	cases := []struct {
		name     string
		statuses map[string]string
		aal      string
		listErr  error
		want     int
	}{
		{"first factor at aal1", nil, "aal1", nil, 200},
		{"pending factor at aal1", map[string]string{"f0": "unverified"}, "aal1", nil, 200},
		{"verified factor at aal1", map[string]string{"f0": "verified"}, "aal1", nil, 403},
		{"verified factor, no aal claim", map[string]string{"f0": "verified"}, "", nil, 403},
		{"verified factor at aal2", map[string]string{"f0": "verified"}, "aal2", nil, 200},
		{"list error", nil, "aal1", errors.New("db down"), 500},
	}
	for _, tc := range cases {
		m := newMFAHarness(t, factorsSvc(tc.statuses, tc.listErr)).as(tc.aal, "s")
		w := m.do("POST", "/auth/v1/factors", `{"factor_type":"totp"}`)
		if w.Code != tc.want {
			t.Errorf("%s: status %d want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
		if tc.want == 403 && !strings.Contains(w.Body.String(), "insufficient_aal") {
			t.Errorf("%s: want insufficient_aal, got %s", tc.name, w.Body.String())
		}
	}
}

func TestMFA_UnenrollVerifiedRequiresAAL2(t *testing.T) {
	cases := []struct {
		name, target, aal string
		want              int
	}{
		{"verified at aal1", "fv", "aal1", 422},
		{"verified at aal2", "fv", "aal2", 200},
		{"unverified at aal1", "fu", "aal1", 200},
		{"unknown factor", "nope", "aal1", 404},
	}
	for _, tc := range cases {
		deleted := false
		var gotAllowVerified bool
		svc := factorsSvc(map[string]string{"fv": "verified", "fu": "unverified"}, nil)
		svc.deleteFactorForUserFn = func(ctx context.Context, fid, uid string, allowVerified bool) error {
			gotAllowVerified = allowVerified
			if fid == "nope" {
				return domain.ErrNotFound
			}
			deleted = true
			return nil
		}
		w := newMFAHarness(t, svc).as(tc.aal, "s").do("DELETE", "/auth/v1/factors/"+tc.target, ``)
		if w.Code != tc.want {
			t.Errorf("%s: status %d want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
		if tc.want == 422 && (deleted || !strings.Contains(w.Body.String(), "insufficient_aal")) {
			t.Errorf("%s: deleted=%v body %s", tc.name, deleted, w.Body.String())
		}
		if tc.want != 422 && gotAllowVerified != (tc.aal == "aal2") {
			t.Errorf("%s: allowVerified=%v, want %v", tc.name, gotAllowVerified, tc.aal == "aal2")
		}
	}
	svc := factorsSvc(nil, errors.New("db down"))
	if w := newMFAHarness(t, svc).as("aal1", "s").do("DELETE", "/auth/v1/factors/fv", ``); w.Code != 500 {
		t.Errorf("list error: status %d", w.Code)
	}
}

func TestGetUser_ExposesFactorsForSupabaseJS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, withFactor := range []bool{true, false} {
		svc := factorsSvc(nil, nil)
		if withFactor {
			svc = factorsSvc(map[string]string{"f1": "verified"}, nil)
		}
		svc.getUserByIDFn = func(ctx context.Context, id string) (map[string]any, error) { return testUserRow(id), nil }
		km := stubKeys(t)
		h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{}}, authSvc: svc, jwtKeys: km,
			logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		r := gin.New()
		r.GET("/auth/v1/user", jwtAuth(km, true), h.handleGetUser)
		req := httptest.NewRequest("GET", "/auth/v1/user", nil)
		req.Header.Set("Authorization", "Bearer "+signToken(t, km, jwt.MapClaims{
			"sub": "u1", "role": "authenticated", "aud": "authenticated", "exp": time.Now().Add(time.Hour).Unix()}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		factors, has := body["factors"].([]any)
		if withFactor && (!has || len(factors) != 1 || factors[0].(map[string]any)["factor_type"] != "totp" || factors[0].(map[string]any)["status"] != "verified") {
			t.Errorf("factors = %v", body["factors"])
		}
		if !withFactor && body["factors"] != nil {
			t.Errorf("empty factors must be omitted like GoTrue, got %v", body["factors"])
		}
	}
}

func TestMFA_ChallengeRateLimited(t *testing.T) {
	svc := &stubAuthService{
		createChallengeFn: func(ctx context.Context, factorID, userID string) (string, time.Time, error) {
			return "", time.Time{}, domain.ErrChallengeRateLimited
		},
	}
	w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/challenge", ``)
	if w.Code != 429 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "over_request_rate_limit" {
		t.Errorf("code = %v, want over_request_rate_limit", body["code"])
	}
}

func TestMFA_ChallengeUnknownFactor(t *testing.T) {
	svc := &stubAuthService{
		createChallengeFn: func(ctx context.Context, factorID, userID string) (string, time.Time, error) {
			return "", time.Time{}, domain.ErrNotFound
		},
	}
	w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/challenge", ``)
	if w.Code != 404 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestMFA_ChallengeCreatesOK(t *testing.T) {
	now := time.Now()
	svc := &stubAuthService{
		createChallengeFn: func(ctx context.Context, factorID, userID string) (string, time.Time, error) {
			return "c1", now, nil
		},
	}
	w := newMFAHarness(t, svc).do("POST", "/auth/v1/factors/f1/challenge", ``)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["id"] != "c1" {
		t.Errorf("id = %v", body["id"])
	}
}

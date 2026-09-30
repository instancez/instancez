package http

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"

	adapterauth "github.com/instancez/instancez/internal/adapter/auth"
	"github.com/instancez/instancez/internal/domain"
)

func bannedRow(banned any) map[string]any {
	row := testUserRow("u1")
	if banned != nil {
		row["is_banned"] = banned
	}
	return row
}

func TestBannedUser_NoSessionOnAnyGrant(t *testing.T) {
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	grants := map[string]struct {
		path, body string
		svc        func(row map[string]any) *stubAuthService
	}{
		"password": {"/auth/v1/token?grant_type=password", `{"email":"u@e.com","password":"pw"}`,
			func(row map[string]any) *stubAuthService {
				return &stubAuthService{verifyPasswordFn: func(context.Context, string, string) (map[string]any, error) { return row, nil }}
			}},
		"refresh": {"/auth/v1/token?grant_type=refresh_token", `{"refresh_token":"rt"}`,
			func(row map[string]any) *stubAuthService {
				return &stubAuthService{consumeRefreshFn: func(context.Context, string, domain.RefreshRotation) (map[string]any, domain.SessionMeta, string, error) {
					return row, domain.SessionMeta{}, "rt2", nil
				}}
			}},
		"pkce": {"/auth/v1/token?grant_type=pkce", `{"auth_code":"ac","code_verifier":"` + verifier + `"}`,
			func(row map[string]any) *stubAuthService {
				return &stubAuthService{
					getPKCEFlowStateFn: func(context.Context, string) (string, string, string, error) { return challenge, "s256", "u1", nil },
					getUserByIDFn:      func(context.Context, string) (map[string]any, error) { return row, nil },
				}
			}},
	}
	for name, g := range grants {
		for _, tc := range []struct {
			banned any
			want   int
		}{{true, 403}, {false, 200}, {nil, 200}} {
			w := postJSON(newTokenRouter(t, g.svc(bannedRow(tc.banned)), nil), g.path, g.body)
			if w.Code != tc.want {
				t.Errorf("%s banned=%v: status %d want %d: %s", name, tc.banned, w.Code, tc.want, w.Body.String())
			}
			if tc.want == 403 && !strings.Contains(w.Body.String(), "user_banned") {
				t.Errorf("%s: want user_banned code, got %s", name, w.Body.String())
			}
		}
	}
}

func TestBannedUser_RecoveryLinkRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{JWTExpiry: "15m"}}, jwtKeys: stubKeys(t),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		authSvc: &stubAuthService{
			peekOneTimeTokenFn: func(context.Context, string) (domain.OTPRow, error) {
				return domain.OTPRow{UserID: "u1", Purpose: "recovery"}, nil
			},
			getUserByIDFn: func(context.Context, string) (map[string]any, error) { return bannedRow(true), nil },
		}}
	r := gin.New()
	r.GET("/auth/v1/verify", h.handleVerifyGET)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/auth/v1/verify?token=t&type=recovery", nil))
	if w.Code != 403 || strings.Contains(w.Header().Get("Location"), "access_token") {
		t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
	}
}

func TestAdminUpdateUser_BanRevokesSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The stub's is_banned reflects banned_until after the write, like the real UpdateUser.
	for body, wantRevoke := range map[string]bool{
		`{"ban_duration":"24h"}`:  true,
		`{"ban_duration":"none"}`: false,
		`{"user_metadata":{}}`:    false,
		// A non-positive duration leaves the row unbanned, so nothing is revoked.
		`{"ban_duration":"0s"}`: false,
	} {
		revoked := ""
		h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			authSvc: &stubAuthService{
				updateUserFn: func(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error) {
					banned := p.BanDuration != nil && *p.BanDuration != "none" && *p.BanDuration != "0s"
					return bannedRow(banned), nil
				},
				revokeAllUserSessionsFn: func(ctx context.Context, uid string) error { revoked = uid; return nil },
			}}
		r := gin.New()
		r.PUT("/admin/users/:uid", h.handleAdminUpdateUser)
		req := httptest.NewRequest("PUT", "/admin/users/u9", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 200 || (revoked == "u9") != wantRevoke {
			t.Errorf("%s: status %d revoked=%q want revoke=%v", body, w.Code, revoked, wantRevoke)
		}
	}
}

func TestBannedUser_MagiclinkVerifyRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{JWTExpiry: "1h", Email: &domain.AuthEmail{}}},
		authSvc: &stubAuthService{
			verifyOTPFn: func(context.Context, string, string, []string) (domain.OTPRow, error) {
				return domain.OTPRow{UserID: "u1", Purpose: "magiclink"}, nil
			},
			getUserByIDFn: func(context.Context, string) (map[string]any, error) { return bannedRow(true), nil },
		},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/verify", h.handleVerify)
	w := postJSON(r, "/auth/v1/verify", `{"type":"magiclink","email":"u@e.com","token":"aaaaaaaabbbbbbbbccccccccdddddddd"}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "user_banned") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestBannedUser_MFAVerifyRefused(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	factorID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	svc := &stubAuthService{
		getFactorForVerifyFn: func(context.Context, string, string) (domain.MFAFactor, error) {
			return domain.MFAFactor{Secret: secret, Status: "verified"}, nil
		},
		getUserByIDFn: func(context.Context, string) (map[string]any, error) { return bannedRow(true), nil },
	}
	m := newMFAHarness(t, svc)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	w := m.do("POST", "/auth/v1/factors/"+factorID+"/verify", `{"challenge_id":"c1","code":"`+code+`"}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "user_banned") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

// banOAuthProvider always succeeds so the callback reaches the ban check.
type banOAuthProvider struct{}

func (banOAuthProvider) Name() string { return "banoauth" }
func (banOAuthProvider) AuthorizeURL(_ *domain.OAuthProvider, _ string) string {
	return ""
}
func (banOAuthProvider) ExchangeCode(_ *domain.OAuthProvider, _ string) (string, error) {
	return "tok", nil
}
func (banOAuthProvider) FetchUser(_ string) (*adapterauth.OAuthUserInfo, error) {
	return &adapterauth.OAuthUserInfo{Email: "u@e.com", ProviderID: "p1"}, nil
}

func TestBannedUser_OAuthCallbackRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapterauth.RegisterOAuth(banOAuthProvider{})
	h := &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{
			JWTExpiry: "15m",
			OAuth:     map[string]*domain.OAuthProvider{"banoauth": {ClientID: "cid", ClientSecret: "sec"}},
		}},
		authSvc: &stubAuthService{
			consumeOAuthFlowFn: func(context.Context, string) (domain.FlowState, error) { return domain.FlowState{}, nil },
			upsertOAuthUserFn: func(context.Context, domain.OAuthLogin) (map[string]any, error) {
				return bannedRow(true), nil
			},
		},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	handler := h.handleOAuthCallback("banoauth")
	req := httptest.NewRequest("GET", "/auth/v1/callback/banoauth?state=s1&code=abc", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	handler(c)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "user_banned") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestDashboardDisableUser_SetsBanAndRevokes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var qs []string
	h := &AdminHandler{db: &stubDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		qs = append(qs, q)
		return 1, nil
	}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := gin.New()
	r.POST("/users/:id/disable", h.handleDisableUser)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/users/u1/disable", nil))
	joined := strings.Join(qs, "\n")
	if w.Code != 200 || !strings.Contains(joined, "banned_until = 'infinity'") || !strings.Contains(joined, "DELETE FROM auth.refresh_tokens") {
		t.Fatalf("status %d queries %v", w.Code, qs)
	}
}

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

// ---------- helpers ----------

func TestAsString(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"abc", "abc"},
		{[]byte("xyz"), "xyz"},
		{[16]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00}, "aabbccdd-eeff-1122-3344-556677889900"},
	}
	for _, tc := range tests {
		if got := asString(tc.in); got != tc.want {
			t.Errorf("asString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAsTimeString(t *testing.T) {
	now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)
	if got := asTimeString(now); got != "2026-04-11T12:00:00Z" {
		t.Errorf("asTimeString(time) = %q", got)
	}
	if got := asTimeString(nil); got != "" {
		t.Errorf("asTimeString(nil) = %q, want empty", got)
	}
	if got := asTimeString(time.Time{}); got != "" {
		t.Errorf("asTimeString(zero) = %q, want empty", got)
	}
}

func TestDecodeJSONB(t *testing.T) {
	m := decodeJSONB(`{"foo":"bar"}`)
	if m["foo"] != "bar" {
		t.Errorf("string decode: got %v", m)
	}
	m = decodeJSONB([]byte(`{"x":1}`))
	if v, _ := m["x"].(float64); v != 1 {
		t.Errorf("[]byte decode: got %v", m)
	}
	m = decodeJSONB(map[string]any{"baked": true})
	if m["baked"] != true {
		t.Errorf("passthrough: got %v", m)
	}
	if decodeJSONB(nil) != nil {
		t.Error("nil should decode to nil")
	}
}

// ---------- buildUser shape contract ----------

// TestBuildUser_GoTrueFieldContract is the single most important test in
// this file: supabase-js reads specific field names off the user object and
// silently produces undefined downstream when they're missing. Any change
// that drops one of these fields should fail here.
func TestBuildUser_GoTrueFieldContract(t *testing.T) {
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{}}}
	row := map[string]any{
		"id":                 "11111111-2222-3333-4444-555555555555",
		"email":              "user@example.com",
		"email_verified":     true,
		"email_confirmed_at": time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		"last_sign_in_at":    time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		"raw_app_meta_data":  `{"provider":"email","providers":["email"]}`,
		"raw_user_meta_data": `{"display_name":"Alice"}`,
		"created_at":         time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		"updated_at":         time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
	}
	u := h.buildUser("11111111-2222-3333-4444-555555555555", row)

	required := []string{
		"id", "aud", "role", "email", "email_confirmed_at", "phone",
		"confirmed_at", "last_sign_in_at", "app_metadata", "user_metadata",
		"identities", "created_at", "updated_at",
	}
	for _, k := range required {
		if _, ok := u[k]; !ok {
			t.Errorf("GoTrue user object missing required key: %q", k)
		}
	}
	if u["id"] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("id = %v, want uuid string", u["id"])
	}
	if u["aud"] != "authenticated" {
		t.Errorf("aud = %v", u["aud"])
	}
	if u["role"] != "authenticated" {
		t.Errorf("role = %v", u["role"])
	}
	um, _ := u["user_metadata"].(map[string]any)
	if um["display_name"] != "Alice" {
		t.Errorf("user_metadata missing display_name: %v", um)
	}
	am, _ := u["app_metadata"].(map[string]any)
	if am["provider"] != "email" {
		t.Errorf("app_metadata missing provider: %v", am)
	}
	// confirmed_at should be set when email is verified
	if u["confirmed_at"] == "" {
		t.Error("confirmed_at should be set when email_verified=true")
	}
	// identities must be a slice (not nil) — supabase-js iterates it
	if _, ok := u["identities"].([]any); !ok {
		t.Errorf("identities = %T, want []any", u["identities"])
	}
}

func TestBuildUser_UnverifiedHasEmptyConfirmedAt(t *testing.T) {
	h := &AuthHandler{cfg: &domain.Config{Auth: &domain.Auth{}}}
	row := map[string]any{
		"id":                 "uuid",
		"email":              "u@e.com",
		"email_verified":     false,
		"email_confirmed_at": nil,
		"last_sign_in_at":    nil,
		"raw_app_meta_data":  nil,
		"raw_user_meta_data": nil,
		"created_at":         time.Now(),
		"updated_at":         time.Now(),
	}
	u := h.buildUser("uuid", row)
	if u["confirmed_at"] != "" {
		t.Errorf("confirmed_at should be empty for unverified users, got %v", u["confirmed_at"])
	}
}

// ---------- jwtAuth middleware: GoTrue claim shape ----------

// stubKeys builds a trivial JWTKeyManager backed by an in-memory RS256 key.
func stubKeys(t *testing.T) *app.JWTKeyManager {
	t.Helper()
	km, err := app.NewInMemoryJWTKeyManager("test-kid", nil)
	if err != nil {
		t.Fatalf("stub keys: %v", err)
	}
	return km
}

func signToken(t *testing.T, km *app.JWTKeyManager, claims jwt.MapClaims) string {
	t.Helper()
	key, err := km.Active(context.Background())
	if err != nil {
		t.Fatalf("active key: %v", err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = key.KID
	s, err := tok.SignedString(key.PrivateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestJWTAuth_AcceptsStringSub(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)

	tok := signToken(t, km, jwt.MapClaims{
		"sub":   "11111111-2222-3333-4444-555555555555",
		"role":  "authenticated",
		"aud":   "authenticated",
		"email": "u@e.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	r := gin.New()
	r.GET("/probe", jwtAuth(km, true), func(c *gin.Context) {
		s := getSession(c)
		c.JSON(200, gin.H{"uid": s.UserID, "role": s.Role, "email": s.Email, "auth": s.IsAuthenticated})
	})

	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["uid"] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("uid = %v", body["uid"])
	}
	if body["role"] != "authenticated" {
		t.Errorf("role = %v", body["role"])
	}
	if body["email"] != "u@e.com" {
		t.Errorf("email = %v", body["email"])
	}
	if body["auth"] != true {
		t.Errorf("auth = %v", body["auth"])
	}
}

func TestJWTAuth_RejectsNumericSub(t *testing.T) {
	// Legacy tokens signed with a numeric sub claim must fail validation
	// so users are forced to re-login after the UUID migration.
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)
	tok := signToken(t, km, jwt.MapClaims{
		"sub": 42,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	r := gin.New()
	r.GET("/probe", jwtAuth(km, true), func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401 for numeric sub, got %d", w.Code)
	}
}

func TestJWTAuth_AnonymousSetsAnonRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)
	r := gin.New()
	r.GET("/probe", jwtAuth(km, false), func(c *gin.Context) {
		s := getSession(c)
		c.JSON(200, gin.H{"role": s.Role, "auth": s.IsAuthenticated})
	})
	req := httptest.NewRequest("GET", "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["role"] != "anon" {
		t.Errorf("role = %v", body["role"])
	}
	if body["auth"] != false {
		t.Errorf("auth = %v", body["auth"])
	}
}

// ---------- Mount registration ----------

func TestAuthHandler_Mount_RegistersGoTrueRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{
		cfg: &domain.Config{
			Auth: &domain.Auth{
				RefreshTokens: true,
				Email:         &domain.AuthEmail{VerifyEmail: true},
			},
		},
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	h.Mount(r.Group(""))

	want := map[string]string{
		"POST /auth/v1/signup":                       "",
		"POST /auth/v1/token":                        "",
		"GET /auth/v1/user":                          "",
		"PUT /auth/v1/user":                          "",
		"POST /auth/v1/logout":                       "",
		"POST /auth/v1/recover":                      "",
		"POST /auth/v1/verify":                       "",
		"GET /auth/v1/verify":                        "",
		"POST /auth/v1/otp":                          "",
		"POST /auth/v1/admin/generate_link":          "",
		"POST /auth/v1/admin/users":                  "",
		"GET /auth/v1/admin/users":                   "",
		"GET /auth/v1/admin/users/:uid":              "",
		"PUT /auth/v1/admin/users/:uid":              "",
		"DELETE /auth/v1/admin/users/:uid":           "",
		"POST /auth/v1/invite":                       "",
		"GET /auth/v1/authorize":                     "",
		"GET /auth/v1/settings":                      "",
		"GET /auth/v1/factors":                       "",
		"POST /auth/v1/factors":                      "",
		"DELETE /auth/v1/factors/:factor_id":         "",
		"POST /auth/v1/factors/:factor_id/challenge": "",
		"POST /auth/v1/factors/:factor_id/verify":    "",
	}
	for _, rt := range r.Routes() {
		delete(want, rt.Method+" "+rt.Path)
	}
	for k := range want {
		t.Errorf("missing route: %s", k)
	}
}

func TestCRUDHandler_Mount_RegistersRestV1Routes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{
		cfg: &domain.Config{
			Tables: map[string]domain.Table{
				"todos": {Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
				}},
			},
		},
	}
	r := gin.New()
	h.Mount(r.Group(""))

	paths := map[string]bool{}
	for _, rt := range r.Routes() {
		paths[rt.Method+" "+rt.Path] = true
	}
	required := []string{
		"GET /rest/v1/todos",
		"POST /rest/v1/todos",
		"PATCH /rest/v1/todos",
		"DELETE /rest/v1/todos",
		"HEAD /rest/v1/todos",
		"POST /rest/v1/rpc/:name",
	}
	for _, r := range required {
		if !paths[r] {
			t.Errorf("missing route: %s", r)
		}
	}
}

// ---------- /rest/v1/rpc/:name returns PGRST202 for missing functions ----------

func TestRPCEndpoint_UnknownFunction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{Tables: map[string]domain.Table{}}}
	r := gin.New()
	h.Mount(r.Group(""))

	req := httptest.NewRequest("POST", "/rest/v1/rpc/anything", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// PostgREST returns 404 with code PGRST202 when the function is not
	// in the schema cache; supabase-js exposes this to callers verbatim.
	if w.Code != 404 {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", w.Body.String())
	}
	if body["code"] != "PGRST202" {
		t.Errorf("expected code PGRST202, got %v", body["code"])
	}
	if body["message"] == nil {
		t.Errorf("expected message field, got %v", body)
	}
}

// ---------- handleToken dispatches on grant_type ----------

func TestHandleToken_UnknownGrantType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/token", h.handleToken)

	req := httptest.NewRequest("POST", "/auth/v1/token?grant_type=magic", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400 for unknown grant_type, got %d", w.Code)
	}
}

// ---------- CORS allows supabase-js headers ----------

func TestCORS_AllowsSupabaseJSHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware(domain.CORS{Origins: []string{"*"}}, true))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	allowed := w.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"apikey", "x-client-info", "Content-Profile", "Accept-Profile"} {
		if !strings.Contains(strings.ToLower(allowed), strings.ToLower(h)) {
			t.Errorf("CORS allow-headers missing %q (got %q)", h, allowed)
		}
	}
	exposed := w.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(strings.ToLower(exposed), "content-range") {
		t.Errorf("CORS expose-headers missing Content-Range (got %q)", exposed)
	}
}

// small helper to silence unused import complaints when ctx is threaded
var _ http.Handler = (http.HandlerFunc)(nil)

// ---------- stub DB for auth handler tests ----------

// stubDB implements domain.Database with hookable QueryRow / Exec
// responses. Only the methods the auth handlers touch are populated;
// the rest return zero values.
type stubDB struct {
	queryRowFn func(ctx context.Context, q string, args ...any) (map[string]any, error)
	queryFn    func(ctx context.Context, q string, args ...any) ([]map[string]any, error)
	execFn     func(ctx context.Context, q string, args ...any) (int64, error)
}

func (s *stubDB) Close() error                                    { return nil }
func (s *stubDB) Ping(ctx context.Context) error                  { return nil }
func (s *stubDB) EnsureMigrationsTable(ctx context.Context) error { return nil }
func (s *stubDB) GetLastMigration(ctx context.Context) (*domain.Migration, error) {
	return nil, nil
}
func (s *stubDB) RecordMigration(ctx context.Context, checksum, sql, configJSON string) error {
	return nil
}
func (s *stubDB) ExecDDL(ctx context.Context, sql string) error { return nil }
func (s *stubDB) EnsureDataTable(ctx context.Context) error     { return nil }
func (s *stubDB) GetAppliedData(ctx context.Context) ([]domain.DataRecord, error) {
	return nil, nil
}
func (s *stubDB) RecordData(ctx context.Context, tx domain.Tx, key, tableName, source, checksum string, rowCount int) error {
	return nil
}
func (s *stubDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	if s.queryFn != nil {
		return s.queryFn(ctx, q, args...)
	}
	return nil, nil
}
func (s *stubDB) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	if s.queryRowFn != nil {
		return s.queryRowFn(ctx, q, args...)
	}
	return nil, nil
}
func (s *stubDB) Exec(ctx context.Context, q string, args ...any) (int64, error) {
	if s.execFn != nil {
		return s.execFn(ctx, q, args...)
	}
	return 0, nil
}
func (s *stubDB) WithRLS(ctx context.Context, session domain.Session) (context.Context, error) {
	return ctx, nil
}
func (s *stubDB) Begin(ctx context.Context) (domain.Tx, error) { return nil, nil }

// ---------- stubAuthService ----------

// stubAuthService implements domain.AuthService with hookable function fields.
// Only the methods a given test exercises need non-nil functions; everything
// else has a safe no-op default.
type stubAuthService struct {
	createUserFn          func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error)
	getUserByEmailFn      func(ctx context.Context, email string) (map[string]any, error)
	getUserIDByEmailFn    func(ctx context.Context, email string) (string, error)
	getUserByIDFn         func(ctx context.Context, id string) (map[string]any, error)
	updateUserFn          func(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error)
	deleteUserFn          func(ctx context.Context, id string) error
	listUsersFn           func(ctx context.Context, page, perPage int) ([]map[string]any, int, error)
	verifyPasswordFn      func(ctx context.Context, email, password string) (map[string]any, error)
	getUserEmailFn        func(ctx context.Context, userID string) (string, error)
	hasPasswordFn         func(ctx context.Context, userID string) (bool, error)
	consumeRefreshFn      func(ctx context.Context, token string) (map[string]any, error)
	createOneTimeTokenFn  func(ctx context.Context, userID, token, purpose string, expiresAt int64) error
	createOTPCodeFn       func(ctx context.Context, userID, token, code, email, purpose string, expiresAt int64) error
	verifyOTPFn           func(ctx context.Context, token, email string, allowedPurposes []string) (domain.OTPRow, error)
	peekOneTimeTokenFn    func(ctx context.Context, token string) (domain.OTPRow, error)
	deleteOneTimeTokenFn  func(ctx context.Context, token string) error
	markEmailVerifiedFn   func(ctx context.Context, userID string)
	insertRefreshTokenFn  func(ctx context.Context, userID, token string, meta domain.SessionMeta, expiresAt int64) error
	getPKCEFlowStateFn    func(ctx context.Context, authCode string) (string, string, string, error)
	countIdentitiesFn     func(ctx context.Context, userID string) (int, error)
	deleteIdentityByIDFn  func(ctx context.Context, identityID, userID string) error
	upsertOAuthUserFn     func(ctx context.Context, provider, providerUserID, email, name string) (map[string]any, error)
	listIdentitiesFn      func(ctx context.Context, userID string) ([]map[string]any, error)
	consumeOAuthFlowFn    func(ctx context.Context, state string) (domain.FlowState, error)
	deleteFactorForUserFn func(ctx context.Context, factorID, userID string) error

	enrollFactorFn            func(ctx context.Context, userID, friendlyName, secret string) (string, error)
	createChallengeFn         func(ctx context.Context, factorID, userID string) (string, time.Time, error)
	getFactorForVerifyFn      func(ctx context.Context, factorID, userID string) (domain.MFAFactor, error)
	validateChallengeFn       func(ctx context.Context, challengeID, factorID string) error
	markChallengeVerifiedFn   func(ctx context.Context, challengeID string) error
	promoteFactorToVerifiedFn func(ctx context.Context, factorID string) error
	listFactorsFn             func(ctx context.Context, userID string) ([]map[string]any, error)
}

// Compile-time check that stubAuthService satisfies the interface.
var _ domain.AuthService = (*stubAuthService)(nil)

func (s *stubAuthService) CreateUser(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
	if s.createUserFn != nil {
		return s.createUserFn(ctx, p)
	}
	return map[string]any{"id": "user-1", "email": p.Email}, nil
}
func (s *stubAuthService) GetUserByID(ctx context.Context, id string) (map[string]any, error) {
	if s.getUserByIDFn != nil {
		return s.getUserByIDFn(ctx, id)
	}
	return nil, domain.ErrNotFound
}
func (s *stubAuthService) GetUserByEmail(ctx context.Context, email string) (map[string]any, error) {
	if s.getUserByEmailFn != nil {
		return s.getUserByEmailFn(ctx, email)
	}
	return nil, domain.ErrNotFound
}
func (s *stubAuthService) GetUserIDByEmail(ctx context.Context, email string) (string, error) {
	if s.getUserIDByEmailFn != nil {
		return s.getUserIDByEmailFn(ctx, email)
	}
	return "", domain.ErrNotFound
}
func (s *stubAuthService) UpdateUser(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error) {
	if s.updateUserFn != nil {
		return s.updateUserFn(ctx, id, p)
	}
	return map[string]any{"id": id}, nil
}
func (s *stubAuthService) DeleteUser(ctx context.Context, id string) error {
	if s.deleteUserFn != nil {
		return s.deleteUserFn(ctx, id)
	}
	return nil
}
func (s *stubAuthService) ListUsers(ctx context.Context, page, perPage int) ([]map[string]any, int, error) {
	if s.listUsersFn != nil {
		return s.listUsersFn(ctx, page, perPage)
	}
	return nil, 0, nil
}
func (s *stubAuthService) VerifyPassword(ctx context.Context, email, password string) (map[string]any, error) {
	if s.verifyPasswordFn != nil {
		return s.verifyPasswordFn(ctx, email, password)
	}
	return nil, domain.ErrUnauthorized
}
func (s *stubAuthService) GetUserEmail(ctx context.Context, userID string) (string, error) {
	if s.getUserEmailFn != nil {
		return s.getUserEmailFn(ctx, userID)
	}
	return "", domain.ErrNotFound
}
func (s *stubAuthService) HasPassword(ctx context.Context, userID string) (bool, error) {
	if s.hasPasswordFn != nil {
		return s.hasPasswordFn(ctx, userID)
	}
	return false, nil
}
func (s *stubAuthService) RecordSignIn(ctx context.Context, userID string) {}
func (s *stubAuthService) InsertRefreshToken(ctx context.Context, userID, token string, meta domain.SessionMeta, expiresAt int64) error {
	if s.insertRefreshTokenFn != nil {
		return s.insertRefreshTokenFn(ctx, userID, token, meta, expiresAt)
	}
	return nil
}
func (s *stubAuthService) ConsumeRefreshToken(ctx context.Context, token string) (map[string]any, error) {
	if s.consumeRefreshFn != nil {
		return s.consumeRefreshFn(ctx, token)
	}
	return nil, domain.ErrUnauthorized
}
func (s *stubAuthService) RevokeSessionByID(ctx context.Context, sessionID string) error      { return nil }
func (s *stubAuthService) RevokeOtherSessions(ctx context.Context, userID, keep string) error { return nil }
func (s *stubAuthService) RevokeAllUserSessions(ctx context.Context, userID string) error     { return nil }
func (s *stubAuthService) CreateOneTimeToken(ctx context.Context, userID, token, purpose string, expiresAt int64) error {
	if s.createOneTimeTokenFn != nil {
		return s.createOneTimeTokenFn(ctx, userID, token, purpose, expiresAt)
	}
	return nil
}
func (s *stubAuthService) CreateOTPCode(ctx context.Context, userID, token, code, email, purpose string, expiresAt int64) error {
	if s.createOTPCodeFn != nil {
		return s.createOTPCodeFn(ctx, userID, token, code, email, purpose, expiresAt)
	}
	return nil
}
func (s *stubAuthService) DeleteUserTokensByPurpose(ctx context.Context, userID, purpose string) error {
	return nil
}
func (s *stubAuthService) DeleteOneTimeToken(ctx context.Context, token string) error {
	if s.deleteOneTimeTokenFn != nil {
		return s.deleteOneTimeTokenFn(ctx, token)
	}
	return nil
}
func (s *stubAuthService) VerifyOTP(ctx context.Context, token, email string, allowedPurposes []string) (domain.OTPRow, error) {
	if s.verifyOTPFn != nil {
		return s.verifyOTPFn(ctx, token, email, allowedPurposes)
	}
	return domain.OTPRow{}, domain.ErrInvalidToken
}
func (s *stubAuthService) PeekOneTimeToken(ctx context.Context, token string) (domain.OTPRow, error) {
	if s.peekOneTimeTokenFn != nil {
		return s.peekOneTimeTokenFn(ctx, token)
	}
	return domain.OTPRow{}, domain.ErrInvalidToken
}
func (s *stubAuthService) MarkEmailVerified(ctx context.Context, userID string) {
	if s.markEmailVerifiedFn != nil {
		s.markEmailVerifiedFn(ctx, userID)
	}
}
func (s *stubAuthService) GetPKCEFlowState(ctx context.Context, authCode string) (string, string, string, error) {
	if s.getPKCEFlowStateFn != nil {
		return s.getPKCEFlowStateFn(ctx, authCode)
	}
	return "", "", "", domain.ErrInvalidToken
}
func (s *stubAuthService) DeletePKCEFlowState(ctx context.Context, authCode string) error { return nil }
func (s *stubAuthService) CreatePKCEFlowState(ctx context.Context, authCode, userID, codeChallenge, method string) error {
	return nil
}
func (s *stubAuthService) CreateOAuthFlowState(ctx context.Context, state, codeChallenge, method, redirectTo, linkingUserID string) error {
	return nil
}
func (s *stubAuthService) ConsumeOAuthFlowState(ctx context.Context, state string) (domain.FlowState, error) {
	if s.consumeOAuthFlowFn != nil {
		return s.consumeOAuthFlowFn(ctx, state)
	}
	return domain.FlowState{}, domain.ErrNotFound
}
func (s *stubAuthService) UpsertOAuthUser(ctx context.Context, provider, providerUserID, email, name string) (map[string]any, error) {
	if s.upsertOAuthUserFn != nil {
		return s.upsertOAuthUserFn(ctx, provider, providerUserID, email, name)
	}
	return map[string]any{"id": "user-1"}, nil
}
func (s *stubAuthService) LinkIdentity(ctx context.Context, userID, provider, providerUserID, email string) {
}
func (s *stubAuthService) ListIdentities(ctx context.Context, userID string) ([]map[string]any, error) {
	if s.listIdentitiesFn != nil {
		return s.listIdentitiesFn(ctx, userID)
	}
	return nil, nil
}
func (s *stubAuthService) CountIdentities(ctx context.Context, userID string) (int, error) {
	if s.countIdentitiesFn != nil {
		return s.countIdentitiesFn(ctx, userID)
	}
	return 0, nil
}
func (s *stubAuthService) DeleteIdentityByID(ctx context.Context, identityID, userID string) error {
	if s.deleteIdentityByIDFn != nil {
		return s.deleteIdentityByIDFn(ctx, identityID, userID)
	}
	return nil
}
func (s *stubAuthService) DeleteFactorForUser(ctx context.Context, factorID, userID string) error {
	if s.deleteFactorForUserFn != nil {
		return s.deleteFactorForUserFn(ctx, factorID, userID)
	}
	return nil
}
func (s *stubAuthService) EnrollFactor(ctx context.Context, userID, friendlyName, secret string) (string, error) {
	if s.enrollFactorFn != nil {
		return s.enrollFactorFn(ctx, userID, friendlyName, secret)
	}
	return "factor-1", nil
}
func (s *stubAuthService) CreateChallenge(ctx context.Context, factorID, userID string) (string, time.Time, error) {
	if s.createChallengeFn != nil {
		return s.createChallengeFn(ctx, factorID, userID)
	}
	return "challenge-1", time.Now(), nil
}
func (s *stubAuthService) GetFactorForVerify(ctx context.Context, factorID, userID string) (domain.MFAFactor, error) {
	if s.getFactorForVerifyFn != nil {
		return s.getFactorForVerifyFn(ctx, factorID, userID)
	}
	return domain.MFAFactor{}, domain.ErrNotFound
}
func (s *stubAuthService) ValidateChallenge(ctx context.Context, challengeID, factorID string) error {
	if s.validateChallengeFn != nil {
		return s.validateChallengeFn(ctx, challengeID, factorID)
	}
	return nil
}
func (s *stubAuthService) MarkChallengeVerified(ctx context.Context, challengeID string) error {
	if s.markChallengeVerifiedFn != nil {
		return s.markChallengeVerifiedFn(ctx, challengeID)
	}
	return nil
}
func (s *stubAuthService) PromoteFactorToVerified(ctx context.Context, factorID string) error {
	if s.promoteFactorToVerifiedFn != nil {
		return s.promoteFactorToVerifiedFn(ctx, factorID)
	}
	return nil
}
func (s *stubAuthService) ListFactors(ctx context.Context, userID string) ([]map[string]any, error) {
	if s.listFactorsFn != nil {
		return s.listFactorsFn(ctx, userID)
	}
	return nil, nil
}

// ---------- signup dispatch / anonymous ----------

// TestHandleSignupDispatch_AnonymousOnEmptyBody asserts that POSTing an
// empty JSON body to /signup routes to the anonymous path instead of
// being rejected by the `required,email` binding on the struct.
func TestHandleSignupDispatch_AnonymousOnEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubAuthService{
		createUserFn: func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
			if !p.Anonymous {
				t.Errorf("expected anonymous create, got %+v", p)
			}
			return map[string]any{
				"id":                 "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"email":              nil,
				"email_verified":     false,
				"raw_app_meta_data":  `{"is_anonymous":true}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
				"is_anonymous":       true,
			}, nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{JWTExpiry: "15m"}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/signup", h.handleSignupDispatch)

	req := httptest.NewRequest("POST", "/auth/v1/signup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	tok, _ := body["access_token"].(string)
	if tok == "" {
		t.Fatalf("missing access_token in body: %s", w.Body.String())
	}
	// Parse claims and verify is_anonymous=true.
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if isAnon, _ := claims["is_anonymous"].(bool); !isAnon {
		t.Errorf("expected is_anonymous=true in claims, got %v", claims["is_anonymous"])
	}
}

// ---------- signup gating (allow_signup / allow_anonymous) ----------

func signupGatingHandler(t *testing.T, allowSignup, allowAnonymous *bool) (*AuthHandler, *stubAuthService) {
	t.Helper()
	svc := &stubAuthService{
		createUserFn: func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
			return map[string]any{
				"id":                 "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"email":              "user@example.com",
				"email_verified":     false,
				"raw_app_meta_data":  `{}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
	}
	h := &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{
			JWTExpiry:      "15m",
			AllowSignup:    allowSignup,
			AllowAnonymous: allowAnonymous,
		}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	return h, svc
}

func ptrBool(b bool) *bool { return &b }

func postSignup(h *AuthHandler, body string) *httptest.ResponseRecorder {
	r := gin.New()
	r.POST("/auth/v1/signup", h.handleSignupDispatch)
	req := httptest.NewRequest("POST", "/auth/v1/signup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSignupGating_AllowSignupFalse_BlocksEmailSignup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := signupGatingHandler(t, ptrBool(false), nil)
	w := postSignup(h, `{"email":"u@example.com","password":"hunter2hunter2"}`)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "signup_disabled" {
		t.Errorf("expected code=signup_disabled, got %v (body=%s)", body["code"], w.Body.String())
	}
}

// allow_signup=false implies anonymous is also blocked (anonymous users
// are new user rows). This invariant is part of the YAML contract.
func TestSignupGating_AllowSignupFalse_BlocksAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := signupGatingHandler(t, ptrBool(false), nil)
	w := postSignup(h, `{}`)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "signup_disabled" {
		t.Errorf("expected code=signup_disabled, got %v (body=%s)", body["code"], w.Body.String())
	}
}

func TestSignupGating_AnonymousFalse_BlocksAnonymousOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := signupGatingHandler(t, ptrBool(true), ptrBool(false))
	w := postSignup(h, `{}`)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "signup_disabled" {
		t.Errorf("expected code=signup_disabled, got %v (body=%s)", body["code"], w.Body.String())
	}
}

func TestSignupGating_AnonymousFalse_AllowsEmailSignup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := signupGatingHandler(t, ptrBool(true), ptrBool(false))
	w := postSignup(h, `{"email":"u@example.com","password":"hunter2hunter2"}`)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSignupGating_NilFlags_PreservesBackwardCompat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// email signup
	h, _ := signupGatingHandler(t, nil, nil)
	w := postSignup(h, `{"email":"u@example.com","password":"hunter2hunter2"}`)
	if w.Code != 200 {
		t.Fatalf("email signup with nil flags: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// anonymous
	h2, _ := signupGatingHandler(t, nil, nil)
	w2 := postSignup(h2, `{}`)
	if w2.Code != 200 {
		t.Fatalf("anonymous signup with nil flags: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
}

// Admin-keyed user creation is the escape hatch for admin-only projects;
// it must keep working even with allow_signup=false.
func TestSignupGating_AdminCreateUser_IgnoresAllowSignup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_ADMIN_KEY", "test-admin-key")

	h, _ := signupGatingHandler(t, ptrBool(false), ptrBool(false))
	r := gin.New()
	h.Mount(r.Group(""))

	req := httptest.NewRequest("POST", "/auth/v1/admin/users",
		strings.NewReader(`{"email":"new@example.com","password":"hunter2hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-admin-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Admin-create must not be rejected with signup_disabled. Accept any
	// 2xx (handler returns 200 on success); reject 403.
	if w.Code == 403 {
		t.Fatalf("admin create user blocked by signup gating: %s", w.Body.String())
	}
	if w.Code >= 400 {
		t.Fatalf("admin create user failed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSignupGating_AdminInvite_IgnoresAllowSignup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_ADMIN_KEY", "test-admin-key")

	h, _ := signupGatingHandler(t, ptrBool(false), ptrBool(false))
	r := gin.New()
	h.Mount(r.Group(""))

	req := httptest.NewRequest("POST", "/auth/v1/invite",
		strings.NewReader(`{"email":"invited@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-admin-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == 403 {
		t.Fatalf("admin invite blocked by signup gating: %s", w.Body.String())
	}
	if w.Code >= 400 {
		t.Fatalf("admin invite failed: status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleOTP_CreatesTokenForNewUser drives the magic link path: a
// previously unknown email should produce an INSERT for the user and a
// second INSERT into auth.one_time_tokens with purpose='magiclink'.
func TestHandleOTP_CreatesTokenForNewUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userLookups := 0
	inserts := 0
	insertedPurpose := ""
	svc := &stubAuthService{
		getUserIDByEmailFn: func(ctx context.Context, email string) (string, error) {
			userLookups++
			return "", domain.ErrNotFound
		},
		createUserFn: func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
			return map[string]any{"id": "11111111-2222-3333-4444-555555555555"}, nil
		},
		createOTPCodeFn: func(ctx context.Context, userID, token, code, email, purpose string, expiresAt int64) error {
			inserts++
			insertedPurpose = purpose
			return nil
		},
	}
	h := &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{
			Email: &domain.AuthEmail{},
		}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/otp", h.handleOTP)

	req := httptest.NewRequest("POST", "/auth/v1/otp", strings.NewReader(`{"email":"new@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if userLookups != 1 {
		t.Errorf("expected 1 user lookup, got %d", userLookups)
	}
	if inserts != 1 {
		t.Errorf("expected 1 verification insert, got %d", inserts)
	}
	if insertedPurpose != "magiclink" {
		t.Errorf("expected purpose=magiclink, got %q", insertedPurpose)
	}
}

// TestHandleOTP_NoCreateUserWhenDisabled ensures create_user:false returns
// 200 for unknown emails without inserting a user row (enumeration guard).
func TestHandleOTP_NoCreateUserWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userInserts := 0
	svc := &stubAuthService{
		getUserIDByEmailFn: func(ctx context.Context, email string) (string, error) {
			return "", domain.ErrNotFound
		},
		createUserFn: func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
			userInserts++
			return map[string]any{"id": "x"}, nil
		},
	}
	h := &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{
			Email: &domain.AuthEmail{},
		}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/otp", h.handleOTP)

	req := httptest.NewRequest("POST", "/auth/v1/otp",
		strings.NewReader(`{"email":"ghost@example.com","create_user":false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 (enum guard), got %d", w.Code)
	}
	if userInserts != 0 {
		t.Errorf("expected no user insert when create_user=false, got %d", userInserts)
	}
}

// TestHandleGenerateLink_MagiclinkExistingUser exercises the admin
// generate_link path for a known user: no email is sent, but a magiclink
// token row is created and the response includes action_link + token.
func TestHandleGenerateLink_MagiclinkExistingUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokenInserts := 0
	svc := &stubAuthService{
		getUserByEmailFn: func(ctx context.Context, email string) (map[string]any, error) {
			return map[string]any{
				"id":                 "11111111-2222-3333-4444-555555555555",
				"email":              "user@example.com",
				"email_verified":     true,
				"raw_app_meta_data":  `{}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
		createOneTimeTokenFn: func(ctx context.Context, userID, token, purpose string, expiresAt int64) error {
			tokenInserts++
			return nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/admin/generate_link", h.handleGenerateLink)

	req := httptest.NewRequest("POST", "/auth/v1/admin/generate_link",
		strings.NewReader(`{"type":"magiclink","email":"user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if tokenInserts != 1 {
		t.Errorf("expected 1 token insert, got %d", tokenInserts)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if al, _ := body["action_link"].(string); !strings.Contains(al, "/auth/v1/verify?token=") {
		t.Errorf("missing action_link: %v", body["action_link"])
	}
	if body["verification_type"] != "magiclink" {
		t.Errorf("verification_type = %v", body["verification_type"])
	}
}

// TestGenerateNumericCode_ShapeAndEntropy asserts the code generator
// returns the requested width, only digits, and doesn't return the same
// value on two back-to-back calls.
func TestGenerateNumericCode_ShapeAndEntropy(t *testing.T) {
	got := generateNumericCode(6)
	if len(got) != 6 {
		t.Errorf("length = %d, want 6", len(got))
	}
	for _, c := range got {
		if c < '0' || c > '9' {
			t.Errorf("non-digit %q in code %q", c, got)
		}
	}
	// Probabilistic: two calls returning the same 6-digit value is ~1 in
	// a million. Run a handful and require at least one difference.
	seen := map[string]struct{}{got: {}}
	diff := false
	for i := 0; i < 10; i++ {
		v := generateNumericCode(6)
		if _, ok := seen[v]; !ok {
			diff = true
			break
		}
		seen[v] = struct{}{}
	}
	if !diff {
		t.Errorf("generator produced duplicates 10x in a row — entropy broken")
	}
}

// TestHandleOTP_StoresCodeAndEmail asserts the /otp insert records both a
// 6-digit code and the caller's email so /verify can look up by
// (email, code) in the numeric flow.
func TestHandleOTP_StoresCodeAndEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var capturedCode, capturedEmail string
	svc := &stubAuthService{
		getUserIDByEmailFn: func(ctx context.Context, email string) (string, error) {
			return "11111111-2222-3333-4444-555555555555", nil
		},
		createOTPCodeFn: func(ctx context.Context, userID, token, code, email, purpose string, expiresAt int64) error {
			capturedCode = code
			capturedEmail = email
			return nil
		},
	}
	h := &AuthHandler{
		cfg: &domain.Config{Auth: &domain.Auth{
			Email: &domain.AuthEmail{},
		}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/otp", h.handleOTP)

	req := httptest.NewRequest("POST", "/auth/v1/otp",
		strings.NewReader(`{"email":"otp@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if len(capturedCode) != 6 {
		t.Errorf("expected 6-digit code, got %q", capturedCode)
	}
	if capturedEmail != "otp@example.com" {
		t.Errorf("email = %q, want otp@example.com", capturedEmail)
	}
}

// TestHandleVerify_NumericCode passes the email + 6-digit token through to the
// service's VerifyOTP and, on success, marks the email verified and issues a
// session. The numeric-code lookup, attempt tracking, and token consumption now
// live in the service layer (covered by auth.Service tests).
func TestHandleVerify_NumericCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotToken, gotEmail string
	var gotPurposes []string
	marked := false
	svc := &stubAuthService{
		verifyOTPFn: func(ctx context.Context, token, email string, allowedPurposes []string) (domain.OTPRow, error) {
			gotToken = token
			gotEmail = email
			gotPurposes = allowedPurposes
			return domain.OTPRow{UserID: "11111111-2222-3333-4444-555555555555", Purpose: "magiclink"}, nil
		},
		markEmailVerifiedFn: func(ctx context.Context, userID string) { marked = true },
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"id":                 id,
				"email":              "otp@example.com",
				"email_verified":     true,
				"raw_app_meta_data":  `{}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{JWTExpiry: "1h", Email: &domain.AuthEmail{}}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/verify", h.handleVerify)

	req := httptest.NewRequest("POST", "/auth/v1/verify",
		strings.NewReader(`{"type":"email","email":"otp@example.com","token":"123456"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if gotToken != "123456" || gotEmail != "otp@example.com" {
		t.Errorf("VerifyOTP called with token=%q email=%q", gotToken, gotEmail)
	}
	// type "email" must accept signup + magiclink purposes.
	if len(gotPurposes) != 2 || gotPurposes[0] != "signup" || gotPurposes[1] != "magiclink" {
		t.Errorf("allowed purposes = %v", gotPurposes)
	}
	if !marked {
		t.Error("email verification type should mark the address verified")
	}
}

// TestHandleVerify_InvalidTokenMapsTo401 asserts the service's ErrInvalidToken
// surfaces as a 401 invalid_grant (the brute-force-resistant failure path).
func TestHandleVerify_InvalidTokenMapsTo401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubAuthService{
		verifyOTPFn: func(ctx context.Context, token, email string, allowedPurposes []string) (domain.OTPRow, error) {
			return domain.OTPRow{}, domain.ErrInvalidToken
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{JWTExpiry: "1h", Email: &domain.AuthEmail{}}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/verify", h.handleVerify)

	req := httptest.NewRequest("POST", "/auth/v1/verify",
		strings.NewReader(`{"type":"email","email":"otp@example.com","token":"000000"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("invalid token should be rejected with 401, got %d", w.Code)
	}
}

// TestHandleVerify_MagiclinkType asserts the magiclink verify type accepts any
// stored purpose (nil allowed-set) and does not mark the email verified.
func TestHandleVerify_MagiclinkType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	marked := false
	var gotPurposes []string
	svc := &stubAuthService{
		verifyOTPFn: func(ctx context.Context, token, email string, allowedPurposes []string) (domain.OTPRow, error) {
			gotPurposes = allowedPurposes
			return domain.OTPRow{UserID: "11111111-2222-3333-4444-555555555555", Purpose: "magiclink"}, nil
		},
		markEmailVerifiedFn: func(ctx context.Context, userID string) { marked = true },
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"id":                 id,
				"email":              "u@e.com",
				"email_verified":     true,
				"raw_app_meta_data":  `{}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{JWTExpiry: "1h", Email: &domain.AuthEmail{}}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/verify", h.handleVerify)

	req := httptest.NewRequest("POST", "/auth/v1/verify",
		strings.NewReader(`{"type":"magiclink","email":"u@e.com","token":"aaaaaaaabbbbbbbbccccccccdddddddd"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if gotPurposes != nil {
		t.Errorf("magiclink type should allow any purpose (nil), got %v", gotPurposes)
	}
	if marked {
		t.Error("magiclink verify must not mark the email verified")
	}
}

// TestHandleGenerateLink_UnsupportedType asserts 400 for unknown link types.
func TestHandleGenerateLink_UnsupportedType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: &stubAuthService{},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/admin/generate_link", h.handleGenerateLink)

	req := httptest.NewRequest("POST", "/auth/v1/admin/generate_link",
		strings.NewReader(`{"type":"sms","email":"u@e.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ---------- password reset (recover → verify GET → redirect) ----------

// TestHandleRecover_AlwaysReturns200 verifies email enumeration
// protection: the endpoint returns 200 regardless of whether the email
// exists, and stores a recovery token when the user does exist.
func TestHandleRecover_AlwaysReturns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var storedPurpose string
	svc := &stubAuthService{
		getUserIDByEmailFn: func(ctx context.Context, email string) (string, error) {
			return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", nil
		},
		createOneTimeTokenFn: func(ctx context.Context, userID, token, purpose string, expiresAt int64) error {
			storedPurpose = purpose
			return nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.POST("/auth/v1/recover", h.handleRecover)

	// Known email
	req := httptest.NewRequest("POST", "/auth/v1/recover",
		strings.NewReader(`{"email":"user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200 for known email, got %d", w.Code)
	}
	if storedPurpose != "recovery" {
		t.Errorf("expected recovery token insert, got %q", storedPurpose)
	}

	// Unknown email — still 200
	svc.getUserIDByEmailFn = func(ctx context.Context, email string) (string, error) {
		return "", domain.ErrNotFound
	}
	req2 := httptest.NewRequest("POST", "/auth/v1/recover",
		strings.NewReader(`{"email":"nobody@example.com"}`))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Errorf("expected 200 for unknown email, got %d", w2.Code)
	}
}

// TestHandleVerifyGET_RecoveryRedirectsWithToken exercises the full
// password-reset link-click flow: GET /auth/v1/verify?token=...&type=recovery
// should consume the token, build a session, and redirect with the access
// token in the URL fragment so supabase-js can fire PASSWORD_RECOVERY.
func TestHandleVerifyGET_RecoveryRedirectsWithToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deleted := false
	svc := &stubAuthService{
		peekOneTimeTokenFn: func(ctx context.Context, token string) (domain.OTPRow, error) {
			return domain.OTPRow{UserID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Purpose: "recovery"}, nil
		},
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"id":                 "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"email":              "user@example.com",
				"email_verified":     true,
				"raw_app_meta_data":  `{}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
	}
	svc.deleteOneTimeTokenFn = func(ctx context.Context, token string) error {
		deleted = true
		return nil
	}
	h := &AuthHandler{
		// app.local must be allowlisted, otherwise the redirect (which carries
		// the session tokens) falls back to the base URL — see the
		// disallowed-origin assertion below.
		cfg:     &domain.Config{Auth: &domain.Auth{JWTExpiry: "15m", RedirectURLs: []string{"http://app.local"}}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.GET("/auth/v1/verify", h.handleVerifyGET)

	req := httptest.NewRequest("GET",
		"/auth/v1/verify?token=abc123&type=recovery&redirect_to=http://app.local/reset", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 303 {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://app.local/reset#") {
		t.Fatalf("redirect should point to redirect_to, got %s", loc)
	}
	if !strings.Contains(loc, "access_token=") {
		t.Errorf("redirect missing access_token fragment: %s", loc)
	}
	if !strings.Contains(loc, "type=recovery") {
		t.Errorf("redirect missing type=recovery fragment: %s", loc)
	}
	if !strings.Contains(loc, "refresh_token=") {
		t.Errorf("redirect missing refresh_token fragment: %s", loc)
	}
	if !deleted {
		t.Error("recovery token was not consumed (DELETE not called)")
	}
}

// TestHandleVerifyGET_RecoveryRejectsDisallowedRedirect verifies that an
// attacker-supplied redirect_to that is not on the allowlist does NOT receive
// the recovery session tokens — the redirect falls back to the base URL.
// Regression test for the open-redirect token-leak (account takeover) finding.
func TestHandleVerifyGET_RecoveryRejectsDisallowedRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubAuthService{
		peekOneTimeTokenFn: func(ctx context.Context, token string) (domain.OTPRow, error) {
			return domain.OTPRow{UserID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Purpose: "recovery"}, nil
		},
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"id":                 "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"email":              "user@example.com",
				"email_verified":     true,
				"raw_app_meta_data":  `{}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
	}
	h := &AuthHandler{
		// No allowlist entry for evil.com.
		cfg:     &domain.Config{Auth: &domain.Auth{JWTExpiry: "15m"}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.GET("/auth/v1/verify", h.handleVerifyGET)

	req := httptest.NewRequest("GET",
		"/auth/v1/verify?token=abc123&type=recovery&redirect_to=https://evil.com", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	loc := w.Header().Get("Location")
	if strings.Contains(loc, "evil.com") {
		t.Fatalf("tokens must not be redirected to a disallowed origin, got %s", loc)
	}
	if !strings.Contains(loc, "access_token=") {
		t.Errorf("expected fallback redirect to still carry the fragment, got %s", loc)
	}
}

// TestHandleVerifyGET_ExpiredTokenRejected ensures an expired recovery
// token returns 400 and is cleaned up from the database.
func TestHandleVerifyGET_ExpiredTokenRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deleted := false
	svc := &stubAuthService{
		// PeekOneTimeToken owns the expiry check and the delete-on-expiry; it
		// returns ErrTokenExpired for an expired token.
		peekOneTimeTokenFn: func(ctx context.Context, token string) (domain.OTPRow, error) {
			deleted = true
			return domain.OTPRow{}, domain.ErrTokenExpired
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.GET("/auth/v1/verify", h.handleVerifyGET)

	req := httptest.NewRequest("GET", "/auth/v1/verify?token=expired123&type=recovery", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for expired token, got %d", w.Code)
	}
	if !deleted {
		t.Error("expired token should be cleaned up")
	}
}

// TestHandleVerifyGET_EmailVerificationStillWorks ensures the original
// email verification path is unbroken by the recovery changes.
func TestHandleVerifyGET_EmailVerificationStillWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	emailVerified := false
	svc := &stubAuthService{
		peekOneTimeTokenFn: func(ctx context.Context, token string) (domain.OTPRow, error) {
			return domain.OTPRow{UserID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Purpose: "signup"}, nil
		},
		markEmailVerifiedFn: func(ctx context.Context, userID string) {
			emailVerified = true
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: stubKeys(t),
	}
	r := gin.New()
	r.GET("/auth/v1/verify", h.handleVerifyGET)

	req := httptest.NewRequest("GET", "/auth/v1/verify?token=signuptoken", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !emailVerified {
		t.Error("email_verified should have been set to true")
	}
}

// ---------- handlers that use h.authSvc ----------

// TestHandleGetUser_ReturnsUser verifies that GET /auth/v1/user returns the
// user row from authSvc.GetUserByID when a valid JWT is present.
func TestHandleGetUser_ReturnsUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)

	svc := &stubAuthService{
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"id":                 id,
				"email":              "user@example.com",
				"email_verified":     true,
				"email_confirmed_at": time.Now(),
				"raw_app_meta_data":  `{"provider":"email"}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: km,
	}

	tok := signToken(t, km, jwt.MapClaims{
		"sub":   "11111111-2222-3333-4444-555555555555",
		"role":  "authenticated",
		"aud":   "authenticated",
		"email": "user@example.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	r := gin.New()
	r.GET("/auth/v1/user", jwtAuth(km, true), h.handleGetUser)

	req := httptest.NewRequest("GET", "/auth/v1/user", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["email"] != "user@example.com" {
		t.Errorf("email = %v", body["email"])
	}
}

// TestHandleGetUser_NotFound verifies that GET /auth/v1/user returns 404 when
// authSvc.GetUserByID returns ErrNotFound (the default stub behaviour).
func TestHandleGetUser_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)

	// Default stub returns ErrNotFound for GetUserByID.
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: &stubAuthService{},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: km,
	}

	tok := signToken(t, km, jwt.MapClaims{
		"sub":   "11111111-2222-3333-4444-555555555555",
		"role":  "authenticated",
		"aud":   "authenticated",
		"email": "ghost@example.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	r := gin.New()
	r.GET("/auth/v1/user", jwtAuth(km, true), h.handleGetUser)

	req := httptest.NewRequest("GET", "/auth/v1/user", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleAdminGetUser_ReturnsUser verifies the happy path for
// GET /auth/v1/admin/users/:uid via authSvc.GetUserByID.
func TestHandleAdminGetUser_ReturnsUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &stubAuthService{
		getUserByIDFn: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"id":                 id,
				"email":              "admin@example.com",
				"email_verified":     true,
				"email_confirmed_at": time.Now(),
				"raw_app_meta_data":  `{}`,
				"raw_user_meta_data": `{}`,
				"created_at":         time.Now(),
				"updated_at":         time.Now(),
			}, nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	r := gin.New()
	r.GET("/auth/v1/admin/users/:uid", h.handleAdminGetUser)

	req := httptest.NewRequest("GET", "/auth/v1/admin/users/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["email"] != "admin@example.com" {
		t.Errorf("email = %v", body["email"])
	}
}

// TestHandleAdminGetUser_NotFound verifies that a missing user returns 404.
func TestHandleAdminGetUser_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: &stubAuthService{}, // default: GetUserByID returns ErrNotFound
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	r := gin.New()
	r.GET("/auth/v1/admin/users/:uid", h.handleAdminGetUser)

	req := httptest.NewRequest("GET", "/auth/v1/admin/users/no-such-user", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleAdminListUsers_ReturnsPaginatedUsers verifies the happy path for
// GET /auth/v1/admin/users — checks the users array and x-total-count header.
func TestHandleAdminListUsers_ReturnsPaginatedUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &stubAuthService{
		listUsersFn: func(ctx context.Context, page, perPage int) ([]map[string]any, int, error) {
			return []map[string]any{
				{
					"id":                 "11111111-2222-3333-4444-555555555555",
					"email":              "a@example.com",
					"email_verified":     true,
					"email_confirmed_at": time.Now(),
					"raw_app_meta_data":  `{}`,
					"raw_user_meta_data": `{}`,
					"created_at":         time.Now(),
					"updated_at":         time.Now(),
				},
			}, 1, nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	r := gin.New()
	r.GET("/auth/v1/admin/users", h.handleAdminListUsers)

	req := httptest.NewRequest("GET", "/auth/v1/admin/users", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("x-total-count") != "1" {
		t.Errorf("x-total-count = %q, want 1", w.Header().Get("x-total-count"))
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	users, _ := body["users"].([]any)
	if len(users) != 1 {
		t.Errorf("expected 1 user, got %d", len(users))
	}
	if body["aud"] != "authenticated" {
		t.Errorf("aud = %v", body["aud"])
	}
}

// TestHandleAdminDeleteUser_Success verifies that DELETE /auth/v1/admin/users/:uid
// returns 200 {} on success via authSvc.DeleteUser.
func TestHandleAdminDeleteUser_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	deleted := ""
	svc := &stubAuthService{
		deleteUserFn: func(ctx context.Context, id string) error {
			deleted = id
			return nil
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	r := gin.New()
	r.DELETE("/auth/v1/admin/users/:uid", h.handleAdminDeleteUser)

	req := httptest.NewRequest("DELETE", "/auth/v1/admin/users/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if deleted != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("deleted uid = %q, want aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", deleted)
	}
}

// TestHandleAdminDeleteUser_NotFound verifies that a "not found" error from
// authSvc.DeleteUser maps to a 404 response.
func TestHandleAdminDeleteUser_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &stubAuthService{
		deleteUserFn: func(ctx context.Context, id string) error {
			return fmt.Errorf("user not found")
		},
	}
	h := &AuthHandler{
		cfg:     &domain.Config{Auth: &domain.Auth{}},
		authSvc: svc,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	r := gin.New()
	r.DELETE("/auth/v1/admin/users/:uid", h.handleAdminDeleteUser)

	req := httptest.NewRequest("DELETE", "/auth/v1/admin/users/no-such-user", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

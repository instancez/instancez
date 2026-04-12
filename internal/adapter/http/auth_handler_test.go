package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
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

// stubKey builds a trivial JWTKeyManager backed by an in-memory kid/secret.
func stubKeys(t *testing.T) *app.JWTKeyManager {
	t.Helper()
	km, err := app.NewInMemoryJWTKeyManager("test-kid", []byte("test-secret-must-be-long-enough-to-sign"))
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
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = key.KID
	s, err := tok.SignedString(key.Secret)
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
		"POST /auth/v1/signup":   "",
		"POST /auth/v1/token":    "",
		"GET /auth/v1/user":      "",
		"PUT /auth/v1/user":      "",
		"POST /auth/v1/logout":   "",
		"POST /auth/v1/recover":  "",
		"POST /auth/v1/verify":   "",
		"GET /auth/v1/verify":    "",
		"GET /auth/v1/authorize": "",
		"GET /auth/v1/settings":  "",
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
				"todos": {AllowAnon: true, Fields: map[string]domain.Field{
					"id": {Type: "bigserial", PrimaryKey: true},
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

// ---------- /rest/v1/rpc/:name returns 501 ----------

func TestRPCEndpoint_Returns501(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{Tables: map[string]domain.Table{}}}
	r := gin.New()
	h.Mount(r.Group(""))

	req := httptest.NewRequest("POST", "/rest/v1/rpc/anything", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 501 {
		t.Errorf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
	// Error should use the PostgREST envelope, not a bare string
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", w.Body.String())
	}
	if body["code"] == nil || body["message"] == nil {
		t.Errorf("expected PostgREST envelope, got %v", body)
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

package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func TestCorsMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		cors     domain.CORS
		devMode  bool
		origin   string // request's Origin header; "" omits it
		wantACAO string // expected Access-Control-Allow-Origin; "" means absent
	}{
		{
			name:     "exact origin match is reflected",
			cors:     domain.CORS{Origins: []string{"https://allowed.com"}},
			origin:   "https://allowed.com",
			wantACAO: "https://allowed.com",
		},
		{
			// The requesting origin, not the literal "*", is echoed — since no
			// app can set Access-Control-Allow-Credentials anymore, there is no
			// wildcard-vs-credentials footgun left to guard against.
			name:     "wildcard config reflects the requesting origin",
			cors:     domain.CORS{Origins: []string{"*"}},
			origin:   "https://anything.example",
			wantACAO: "https://anything.example",
		},
		{
			// The core enforcement property: a disallowed origin's own value is
			// never echoed back, so the browser refuses to let that origin's JS
			// read the response.
			name:     "disallowed origin does not get its own value echoed",
			cors:     domain.CORS{Origins: []string{"https://allowed.com"}},
			origin:   "https://evil.example",
			wantACAO: "https://allowed.com",
		},
		{
			name:     "no origins configured, prod: locked down",
			cors:     domain.CORS{},
			devMode:  false,
			origin:   "https://anything.example",
			wantACAO: "",
		},
		{
			name:     "no origins configured, dev: defaults open",
			cors:     domain.CORS{},
			devMode:  true,
			origin:   "http://localhost:5173",
			wantACAO: "http://localhost:5173",
		},
		{
			// No Origin header means this isn't a CORS request at all (e.g. a
			// server-to-server call); browsers never check ACAO in that case.
			name:     "no Origin header, origins configured",
			cors:     domain.CORS{Origins: []string{"https://allowed.com"}},
			origin:   "",
			wantACAO: "https://allowed.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(corsMiddleware(tc.cors, tc.devMode))
			r.GET("/x", func(c *gin.Context) { c.Status(200) })

			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if got := w.Header().Get("Access-Control-Allow-Origin"); got != tc.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantACAO)
			}
			if w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Errorf("Access-Control-Allow-Credentials should never be set (dropped from config)")
			}
		})
	}

	t.Run("OPTIONS preflight is answered without invoking the handler", func(t *testing.T) {
		r := gin.New()
		r.Use(corsMiddleware(domain.CORS{Origins: []string{"https://allowed.com"}}, false))
		called := false
		r.OPTIONS("/x", func(c *gin.Context) { called = true })

		req := httptest.NewRequest(http.MethodOptions, "/x", nil)
		req.Header.Set("Origin", "https://allowed.com")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != 204 {
			t.Errorf("status = %d, want 204", w.Code)
		}
		if called {
			t.Error("handler should not run for a preflight OPTIONS request")
		}
		if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
			t.Error("Access-Control-Allow-Methods should be set")
		}
		if got := w.Header().Get("Access-Control-Max-Age"); got != "86400" {
			t.Errorf("Access-Control-Max-Age = %q, want 86400", got)
		}
	})
}

func TestProfileHeaderGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		method     string
		header     string
		value      string
		wantStatus int
		wantCode   string
	}{
		{"GET no header ok", http.MethodGet, "", "", 200, ""},
		{"GET public ok", http.MethodGet, "Accept-Profile", "public", 200, ""},
		{"GET other rejected", http.MethodGet, "Accept-Profile", "auth", 406, "PGRST106"},
		{"POST no header ok", http.MethodPost, "", "", 200, ""},
		{"POST public ok", http.MethodPost, "Content-Profile", "public", 200, ""},
		{"POST other rejected", http.MethodPost, "Content-Profile", "private", 406, "PGRST106"},
		// Content-Profile on GET is ignored (only Accept-Profile gates reads),
		// and likewise Accept-Profile on POST is ignored.
		{"GET Content-Profile other ignored", http.MethodGet, "Content-Profile", "other", 200, ""},
		{"POST Accept-Profile other ignored", http.MethodPost, "Accept-Profile", "other", 200, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(profileHeaderGuard())
			r.Any("/rest/v1/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

			req := httptest.NewRequest(tc.method, "/rest/v1/x", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("body parse: %v", err)
				}
				if body["code"] != tc.wantCode {
					t.Errorf("code = %v, want %q", body["code"], tc.wantCode)
				}
			}
		})
	}
}

func TestAPIKeyGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := stubKeys(t)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_apikeyguard")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_apikeyguard")

	cases := []struct {
		name       string
		keys       *app.JWTKeyManager
		header     string
		wantStatus int
		wantCode   string
	}{
		{"missing header rejected", km, "", 401, "no_api_key"},
		{"garbage header rejected", km, "not-a-real-key", 401, "invalid_api_key"},
		{"publishable key accepted", km, "inz_publishable_apikeyguard", 200, ""},
		{"secret key accepted", km, "inz_secret_apikeyguard", 200, ""},
		{"nil keys skips the check entirely", nil, "", 200, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(apiKeyGuard(tc.keys))
			r.GET("/probe", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

			req := httptest.NewRequest("GET", "/probe", nil)
			if tc.header != "" {
				req.Header.Set("apikey", tc.header)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("body parse: %v", err)
				}
				if body["code"] != tc.wantCode {
					t.Errorf("code = %v, want %q", body["code"], tc.wantCode)
				}
			}
		})
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("client header accepted and echoed", func(t *testing.T) {
		var seenCtxID string
		var seenGinID string
		r := gin.New()
		r.Use(requestIDMiddleware())
		r.GET("/x", func(c *gin.Context) {
			seenCtxID = domain.RequestIDFromContext(c.Request.Context())
			seenGinID = c.GetString(contextKeyRequestID)
			c.Status(200)
		})

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Request-Id", "client-abc-123")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		if got := w.Header().Get("X-Request-Id"); got != "client-abc-123" {
			t.Errorf("response header = %q, want %q", got, "client-abc-123")
		}
		if seenCtxID != "client-abc-123" {
			t.Errorf("ctx id = %q, want %q", seenCtxID, "client-abc-123")
		}
		if seenGinID != "client-abc-123" {
			t.Errorf("gin id = %q, want %q", seenGinID, "client-abc-123")
		}
	})

	t.Run("missing header generates one", func(t *testing.T) {
		r := gin.New()
		var seen string
		r.Use(requestIDMiddleware())
		r.GET("/x", func(c *gin.Context) {
			seen = c.GetString(contextKeyRequestID)
			c.Status(200)
		})

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if seen == "" {
			t.Fatal("expected generated request id")
		}
		if w.Header().Get("X-Request-Id") != seen {
			t.Errorf("echoed header mismatch: %q vs %q", w.Header().Get("X-Request-Id"), seen)
		}
		if len(seen) != 32 {
			t.Errorf("generated id length = %d, want 32 hex chars", len(seen))
		}
	})

	t.Run("unsafe client id replaced by generated", func(t *testing.T) {
		r := gin.New()
		r.Use(requestIDMiddleware())
		r.GET("/x", func(c *gin.Context) { c.Status(200) })

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Request-Id", "evil'; DROP TABLE users; --")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		got := w.Header().Get("X-Request-Id")
		if got == "evil'; DROP TABLE users; --" {
			t.Errorf("unsafe id was echoed verbatim: %q", got)
		}
		if len(got) != 32 {
			t.Errorf("replacement id length = %d, want 32 hex chars", len(got))
		}
	})

	t.Run("too-long client id rejected", func(t *testing.T) {
		r := gin.New()
		r.Use(requestIDMiddleware())
		r.GET("/x", func(c *gin.Context) { c.Status(200) })

		long := strings.Repeat("a", 200)
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Request-Id", long)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Header().Get("X-Request-Id") == long {
			t.Error("oversized id was echoed")
		}
	})
}

func TestParseSizeBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1KB", 1024},
		{"1MB", 1024 * 1024},
		{"2MB", 2 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"0.5MB", 512 * 1024},
		{"100", 100},
		{"", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSizeBytes(tt.input)
			if got != tt.want {
				t.Errorf("parseSizeBytes(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestComputeHMACSignature(t *testing.T) {
	sig := computeHMACSignature("secret", "12345", `{"data":"test"}`)
	if sig == "" {
		t.Error("expected non-empty signature")
	}
	if len(sig) < 10 {
		t.Error("signature seems too short")
	}
	if sig[:7] != "sha256=" {
		t.Errorf("expected sha256= prefix, got %s", sig[:7])
	}

	// Same inputs produce same output
	sig2 := computeHMACSignature("secret", "12345", `{"data":"test"}`)
	if sig != sig2 {
		t.Error("same inputs should produce same signature")
	}

	// Different inputs produce different output
	sig3 := computeHMACSignature("secret", "12345", `{"data":"other"}`)
	if sig == sig3 {
		t.Error("different inputs should produce different signatures")
	}
}

func TestMatchesMIME(t *testing.T) {
	tests := []struct {
		contentType string
		allowed     []string
		want        bool
	}{
		{"image/png", []string{"image/*"}, true},
		{"image/jpeg", []string{"image/*"}, true},
		{"application/pdf", []string{"image/*"}, false},
		{"application/pdf", []string{"application/pdf"}, true},
		{"image/png", []string{"image/*", "application/pdf"}, true},
		{"application/pdf", []string{"image/*", "application/pdf"}, true},
		{"text/plain", []string{"text/*"}, true},
		{"video/mp4", []string{"image/*", "text/*"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			got := matchesMIME(tt.contentType, tt.allowed)
			if got != tt.want {
				t.Errorf("matchesMIME(%q, %v) = %v, want %v", tt.contentType, tt.allowed, got, tt.want)
			}
		})
	}
}

func TestRenderAuthTemplate(t *testing.T) {
	tests := []struct {
		tmpl string
		vars map[string]string
		want string
	}{
		{
			"Hello {{email}}, click {{link}} to verify.",
			map[string]string{"email": "test@example.com", "link": "http://localhost/verify?token=abc"},
			"Hello test@example.com, click http://localhost/verify?token=abc to verify.",
		},
		{
			"Token: {{token}}",
			map[string]string{"token": "xyz123"},
			"Token: xyz123",
		},
		{
			"No vars here.",
			map[string]string{},
			"No vars here.",
		},
		{
			"{{base_url}}/reset?t={{token}}",
			map[string]string{"base_url": "https://app.example.com", "token": "tok"},
			"https://app.example.com/reset?t=tok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.tmpl, func(t *testing.T) {
			got := renderAuthTemplate(tt.tmpl, tt.vars)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/domain"
)

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

func TestPostgresTypeToOpenAPI(t *testing.T) {
	tests := []struct {
		pgType   string
		wantType string
	}{
		{"bigserial", "integer"},
		{"integer", "integer"},
		{"boolean", "boolean"},
		{"text", "string"},
		{"varchar(255)", "string"},
		{"uuid", "string"},
		{"timestamptz", "string"},
		{"date", "string"},
		{"jsonb", "object"},
		{"numeric(10,2)", "number"},
		{"text[]", "array"},
		{"real", "number"},
	}
	for _, tt := range tests {
		t.Run(tt.pgType, func(t *testing.T) {
			result := postgresTypeToOpenAPI(tt.pgType)
			if result["type"] != tt.wantType {
				t.Errorf("type = %v, want %v", result["type"], tt.wantType)
			}
		})
	}
}

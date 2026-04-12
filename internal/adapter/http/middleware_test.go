package http

import (
	"testing"
)

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

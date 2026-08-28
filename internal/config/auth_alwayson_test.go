package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestApplyDefaults_AuthAlwaysPopulated(t *testing.T) {
	cfg := &domain.Config{} // no auth: block
	ApplyDefaults(cfg)
	if cfg.Auth == nil {
		t.Fatal("Auth must be non-nil after ApplyDefaults (auth is always-on)")
	}
	if cfg.Auth.JWTExpiry == "" {
		t.Errorf("JWTExpiry default not applied: %q", cfg.Auth.JWTExpiry)
	}
	if cfg.Auth.RefreshTokenExpiry == "" {
		t.Errorf("RefreshTokenExpiry default not applied: %q", cfg.Auth.RefreshTokenExpiry)
	}
}

func TestApplyDefaults_EmptyAuthBlockGetsDefaults(t *testing.T) {
	cfg := &domain.Config{Auth: &domain.Auth{}} // auth: {}
	ApplyDefaults(cfg)
	if cfg.Auth.JWTExpiry != "15m" {
		t.Errorf("JWTExpiry = %q, want 15m", cfg.Auth.JWTExpiry)
	}
}

func TestApplyDefaults_RefreshTokensDeprecationWarning(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	no := false
	cfg := &domain.Config{Auth: &domain.Auth{RefreshTokens: &no}}
	ApplyDefaults(cfg)

	if !strings.Contains(buf.String(), "refresh_tokens") {
		t.Errorf("expected deprecation warning mentioning refresh_tokens, got: %q", buf.String())
	}
}

// TestParseBytes_DeprecatedRefreshTokensFalseAccepted proves the strict YAML
// decoder still accepts old config with auth.refresh_tokens: false (no
// unknown-key/type error) and ignores it — refresh tokens stay always-on.
func TestParseBytes_DeprecatedRefreshTokensFalseAccepted(t *testing.T) {
	src := []byte("version: 1\nauth:\n  refresh_tokens: false\n")
	cfg, err := ParseBytes(src, "test.yaml")
	if err != nil {
		t.Fatalf("strict decode should accept deprecated refresh_tokens key, got: %v", err)
	}
	if cfg.Auth == nil {
		t.Fatal("Auth must be non-nil (always-on)")
	}
	if cfg.Auth.RefreshTokens == nil || *cfg.Auth.RefreshTokens != false {
		t.Errorf("RefreshTokens should decode to a pointer to false, got %v", cfg.Auth.RefreshTokens)
	}
	if cfg.Auth.RefreshTokenExpiry == "" {
		t.Error("RefreshTokenExpiry default should still be applied despite the ignored toggle")
	}
}

func TestApplyDefaults_NoWarningWhenRefreshTokensUnset(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	ApplyDefaults(&domain.Config{}) // unset
	if strings.Contains(buf.String(), "refresh_tokens") {
		t.Errorf("no warning expected when refresh_tokens unset, got: %q", buf.String())
	}
}

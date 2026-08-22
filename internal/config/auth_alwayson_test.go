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

package config

import (
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

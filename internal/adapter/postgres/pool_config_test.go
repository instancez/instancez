package postgres

import (
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

func TestParsePoolConfigAppliesIdleTimeout(t *testing.T) {
	cfg, err := parsePoolConfig("postgres://u:p@localhost:5432/db", domain.PoolConfig{
		Max: 20, Min: 5, IdleTimeout: "300s",
	})
	if err != nil {
		t.Fatalf("parsePoolConfig: %v", err)
	}
	if cfg.MaxConnIdleTime != 300*time.Second {
		t.Errorf("MaxConnIdleTime = %v, want 300s", cfg.MaxConnIdleTime)
	}
	if cfg.MaxConns != 20 {
		t.Errorf("MaxConns = %d, want 20", cfg.MaxConns)
	}
	if cfg.MinConns != 5 {
		t.Errorf("MinConns = %d, want 5", cfg.MinConns)
	}
}

func TestParsePoolConfigEmptyIdleTimeoutKeepsPgxDefault(t *testing.T) {
	cfg, err := parsePoolConfig("postgres://u:p@localhost:5432/db", domain.PoolConfig{Max: 4})
	if err != nil {
		t.Fatalf("parsePoolConfig: %v", err)
	}
	// pgxpool's default of 30m applies when idle_timeout is unset.
	if cfg.MaxConnIdleTime != 30*time.Minute {
		t.Errorf("MaxConnIdleTime = %v, want 30m (pgxpool default)", cfg.MaxConnIdleTime)
	}
}

func TestParsePoolConfigRejectsInvalidIdleTimeout(t *testing.T) {
	_, err := parsePoolConfig("postgres://u:p@localhost:5432/db", domain.PoolConfig{IdleTimeout: "banana"})
	if err == nil {
		t.Fatal("expected error for invalid idle_timeout, got nil")
	}
}

func TestParsePoolConfigPercentEncodedAtInPassword(t *testing.T) {
	cfg, err := parsePoolConfig("postgres://u:p%40ss@db.example.com:5432/app", domain.PoolConfig{})
	if err != nil {
		t.Fatalf("parsePoolConfig: %v", err)
	}
	cc := cfg.ConnConfig
	if cc.Password != "p@ss" || cc.Host != "db.example.com" {
		t.Fatalf("got password %q host %q", cc.Password, cc.Host)
	}
}

func TestParsePoolConfigRejectsUnbracketedIPv6(t *testing.T) {
	if _, err := parsePoolConfig("postgres://u:p@::1/db", domain.PoolConfig{}); err == nil {
		t.Fatal("expected error for unbracketed IPv6 host")
	}
}

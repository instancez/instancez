package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func authDefaultCfg(withCol bool) *domain.Config {
	fields := []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}
	if withCol {
		fields = append(fields, domain.Field{Name: "created_by", Type: "uuid", Default: "auth.uid()"})
	}
	return &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{Email: &domain.AuthEmail{}},
		Tables:  map[string]domain.Table{"notes": {Fields: fields}},
	}
}

// A column default may call auth.uid(), so the helper must exist before any table DDL.
func TestAuthHelpersPrecedeTables(t *testing.T) {
	roles := domain.DefaultRoles()
	for name, stmts := range map[string][]string{
		"from scratch": planFromScratchStatements(authDefaultCfg(true), roles),
		"update":       planUpdateStatements(authDefaultCfg(false), authDefaultCfg(true), roles),
	} {
		t.Run(name, func(t *testing.T) {
			fn, tbl, col := -1, -1, -1
			for i, s := range stmts {
				if fn < 0 && strings.Contains(s, "FUNCTION auth.uid()") {
					fn = i
				}
				if tbl < 0 && strings.Contains(s, "CREATE TABLE") && strings.Contains(s, "notes") {
					tbl = i
				}
				if col < 0 && strings.Contains(s, "DEFAULT auth.uid()") {
					col = i
				}
			}
			if fn < 0 {
				t.Fatal("auth.uid() function not emitted")
			}
			if col < 0 {
				t.Fatal("DEFAULT auth.uid() not emitted")
			}
			if fn > col {
				t.Errorf("auth.uid() created at stmt %d, after the default that uses it at %d", fn, col)
			}
			if tbl >= 0 && fn > tbl {
				t.Errorf("auth.uid() created at stmt %d, after CREATE TABLE at %d", fn, tbl)
			}
		})
	}
}

func TestNoAuthBlockEmitsNoHelpers(t *testing.T) {
	cfg := authDefaultCfg(false)
	cfg.Auth = nil
	if strings.Contains(strings.Join(planFromScratchStatements(cfg, domain.DefaultRoles()), "\n"), "FUNCTION auth.uid()") {
		t.Fatal("auth helpers must stay gated on an auth block")
	}
}

// A config without an auth block (the admin save decodes JSON with no defaults) must still get the helper its defaults call.
func TestAuthDefaultWithoutAuthBlockEmitsHelperFirst(t *testing.T) {
	roles := domain.DefaultRoles()
	noAuth := func(withCol bool) *domain.Config {
		cfg := authDefaultCfg(withCol)
		cfg.Auth = nil
		return cfg
	}
	for name, stmts := range map[string][]string{
		"from scratch": planFromScratchStatements(noAuth(true), roles),
		"update":       planUpdateStatements(noAuth(false), noAuth(true), roles),
	} {
		t.Run(name, func(t *testing.T) {
			fn, col := -1, -1
			for i, s := range stmts {
				if fn < 0 && strings.Contains(s, "FUNCTION auth.uid()") {
					fn = i
				}
				if col < 0 && strings.Contains(s, "DEFAULT auth.uid()") {
					col = i
				}
			}
			if fn < 0 || col < 0 || fn > col {
				t.Fatalf("auth.uid() helper at %d, DEFAULT auth.uid() at %d; want the helper first", fn, col)
			}
		})
	}
}

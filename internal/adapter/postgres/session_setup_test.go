package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

func TestBuildSessionSetup_StatementTimeout(t *testing.T) {
	roles := domain.DefaultRoles()
	cases := []struct {
		name  string
		d     time.Duration
		roles *domain.Roles
		want  string // "" = must not set statement_timeout
	}{
		{"request pool", 1500 * time.Millisecond, &roles, "SET LOCAL statement_timeout = 1500;"},
		{"sub-millisecond rounds up, never 0 (0 disables)", 300 * time.Microsecond, &roles, "SET LOCAL statement_timeout = 1;"},
		{"owner pool never (migrations run long DDL)", 1500 * time.Millisecond, nil, ""},
		{"zero", 0, &roles, ""},
		{"negative", -time.Second, &roles, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSessionSetup(domain.ContextWithStatementTimeout(context.Background(), c.d), c.roles)
			if c.want == "" {
				if strings.Contains(got, "statement_timeout") {
					t.Fatalf("unexpected timeout in %q", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("want %q in %q", c.want, got)
			}
		})
	}
}

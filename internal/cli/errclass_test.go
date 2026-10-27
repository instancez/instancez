package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/instancez/instancez/internal/telemetry"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyErr(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("tables: [unclosed\n  : :"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, parseErr := config.LoadWithDotenv(bad, filepath.Join(dir, ".env"))
	_, missingErr := config.LoadWithDotenv(filepath.Join(dir, "nope.yaml"), filepath.Join(dir, ".env"))
	if parseErr == nil || missingErr == nil {
		t.Fatalf("setup: parse=%v missing=%v", parseErr, missingErr)
	}

	tests := []struct {
		name     string
		err      error
		class    string
		expected bool
	}{
		{"nil", nil, "", false},
		{"reported", reported("preflight:dsn"), "preflight:dsn", true},
		{"reported wrapped", fmt.Errorf("x: %w", reported("vet")), "vet", true},
		{"bare sentinel", errReported, "reported", true},
		{"malformed yaml", parseErr, "config_parse", true},
		{"missing file", missingErr, "config_missing", true},
		{"missing env", &domain.MissingEnvError{Vars: []string{"A"}}, "config_invalid", true},
		{"validation errors", fmt.Errorf("w: %w", domain.ValidationErrors{{Path: "p"}}), "config_invalid", true},
		{"destructive", fmt.Errorf("x: %w", app.ErrDestructive), "migrate:destructive", true},
		{"cloud status", fmt.Errorf("deploy: %w", &cloud.APIError{Status: 403}), "cloud_api:403", true},
		{"cloud no status", &cloud.APIError{}, "cloud_api", true},
		{"net op error", fmt.Errorf("db: %w", &net.OpError{}), "go:*net.OpError", false},
		{"plain", errors.New("boom"), "go:*errors.errorString", false},
		{"joined", errors.Join(errors.New("a"), errors.New("b")), "go:*errors.errorString", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, exp, msg := classifyErr(tt.err)
			if class != tt.class || exp != tt.expected {
				t.Fatalf("got (%q, %v), want (%q, %v)", class, exp, tt.class, tt.expected)
			}
			if tt.err != nil && tt.class != "config_invalid" && msg != tt.err.Error() {
				t.Fatalf("msg = %q", msg)
			}
		})
	}
}

func TestReportedStillSilent(t *testing.T) {
	var buf bytes.Buffer
	printFinalErr(&buf, reported("vet"))
	printFinalErr(&buf, fmt.Errorf("wrap: %w", reported("doctor")))
	printFinalErr(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("printed %q", buf.String())
	}
	printFinalErr(&buf, errors.New("boom"))
	if buf.String() != "boom\n" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestUserErrorKeepsMessageAndClass(t *testing.T) {
	inner := errors.New("no x.yaml in this directory")
	err := fmt.Errorf("wrap: %w", userError("config_missing", inner))
	class, expected, msg := classifyErr(err)
	if class != "config_missing" || !expected || msg != "wrap: "+inner.Error() {
		t.Errorf("got %q %v %q", class, expected, msg)
	}
	if !errors.Is(err, inner) {
		t.Error("userError must unwrap to its cause")
	}
}

func TestClassifyCobraErrors(t *testing.T) {
	for msg, want := range map[string]string{
		"unknown command \"x\" for \"inz\"":           "cli_unknown_command",
		"unknown flag: --x":                           "cli_flag",
		"unknown shorthand flag: 'x' in -x":           "cli_flag",
		"accepts at most 1 arg(s), received 2":        "cli_args",
		"requires at least 1 arg(s), only received 0": "cli_args",
		"flag needs an argument: --config":            "cli_flag",
		"invalid argument \"x\" for \"--n\" flag":     "cli_flag",
	} {
		class, expected, _ := classifyErr(errors.New(msg))
		if class != want || !expected {
			t.Errorf("%q: got %q %v", msg, class, expected)
		}
	}
	if class, expected, _ := classifyErr(errors.New("unknown thing")); expected {
		t.Errorf("unrelated message classified expected: %q", class)
	}
}

func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func realRefused(t *testing.T) error {
	t.Helper()
	_, err := net.Dial("tcp", closedAddr(t))
	if err == nil {
		t.Skip("port unexpectedly open")
	}
	return err
}

func TestClassifyEnvErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, inUse := net.Listen("tcp", ln.Addr().String())

	_, pgErr := pgconn.Connect(context.Background(), "postgres://bob:pw@"+closedAddr(t)+"/shop?connect_timeout=2")
	tests := []struct {
		name  string
		err   error
		class string
	}{
		{"refused", realRefused(t), "env:connect_refused"},
		{"refused joined", errors.Join(errors.New("db"), realRefused(t)), "env:connect_refused"},
		{"pgx connect error", fmt.Errorf("database: %w", pgErr), "env:db_connect"},
		{"port in use", fmt.Errorf("http server: %w", inUse), "env:port_in_use"},
		{"dns", fmt.Errorf("db: %w", &net.DNSError{Err: "no such host", Name: "x.invalid"}), "env:dns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("setup produced nil error")
			}
			class, exp, _ := classifyErr(tt.err)
			if class != tt.class || !exp {
				t.Errorf("got (%q, %v), want (%q, true)", class, exp, tt.class)
			}
		})
	}
}

func TestClassifyJoinedUsesFirstLeaf(t *testing.T) {
	err := errors.Join(fmt.Errorf("w: %w", &net.OpError{Op: "x"}), errors.New("b"))
	if class, _, _ := classifyErr(err); class != "go:*net.OpError" {
		t.Errorf("class = %q", class)
	}
	if class, _, _ := classifyErr(emptyMulti{}); class != "go:cli.emptyMulti" {
		t.Errorf("empty join class = %q", class)
	}
}

func TestUnexpectedEnvDoesNotPromptCrash(t *testing.T) {
	r := runCrash(t, "dev", func() error { return realRefused(t) }, "y\n", true, dbg())
	if r.prompted() {
		t.Errorf("env failure prompted:\n%s", r.stderr)
	}
}

func TestRealPgxConnectErrorIsScrubbed(t *testing.T) {
	_, err := pgconn.Connect(context.Background(), "postgres://bob:pw@"+closedAddr(t)+"/shopdb?connect_timeout=2")
	if err == nil {
		t.Fatal("expected connect error")
	}
	msg := telemetry.Sanitize(err.Error(), "")
	for _, leak := range []string{"bob", "shopdb", "pw@"} {
		if strings.Contains(msg, leak) {
			t.Errorf("leaked %q in %q", leak, msg)
		}
	}
}

type emptyMulti struct{}

func (emptyMulti) Error() string   { return "empty" }
func (emptyMulti) Unwrap() []error { return nil }

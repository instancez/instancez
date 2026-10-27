package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

func TestDSNParseErrorsHideCredentials(t *testing.T) {
	for _, dsn := range []string{
		"postgres://saedx1:hunt er2@127.0.0.1:55432/acme_customer_db",
		"postgres://saedx1:hunter2@127.0.0.1:notaport/acme_customer_db",
		"postgres://saedx1:hunter2@[::1/acme_customer_db",
	} {
		_, err1 := passwordFromDSN(dsn)
		_, err2 := withUserPass(dsn, "u", "p", "db")
		for _, err := range []error{err1, err2} {
			if err == nil {
				t.Fatalf("%q parsed", dsn)
			}
			for _, leak := range []string{"saedx1", "hunt", "acme_customer_db", "127.0.0.1"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%q error leaks %q: %v", dsn, leak, err)
				}
			}
		}
	}
}

// fakeAuthFailPG answers every startup with a FATAL error carrying code.
func fakeAuthFailPG(t *testing.T, code string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			be := pgproto3.NewBackend(c, c)
			if _, err := be.ReceiveStartupMessage(); err == nil {
				be.Send(&pgproto3.ErrorResponse{Severity: "FATAL", Code: code, Message: `password authentication failed for user "saedx1"`})
				_ = be.Flush()
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestClassifyConnectErrors(t *testing.T) {
	connect := func(addr string) error {
		_, err := pgconn.Connect(context.Background(), "postgres://saedx1:pw@"+addr+"/shop?sslmode=disable&connect_timeout=2")
		if err == nil {
			t.Fatal("connected")
		}
		return fmt.Errorf("database: %w", err)
	}
	for name, tc := range map[string]struct {
		err   error
		class string
	}{
		"bad password":  {connect(fakeAuthFailPG(t, "28P01")), "env:db_auth"},
		"no such role":  {connect(fakeAuthFailPG(t, "28000")), "env:db_auth"},
		"other startup": {connect(fakeAuthFailPG(t, "3D000")), "env:db_connect"},
		"refused":       {connect(closedAddr(t)), "env:db_connect"},
	} {
		if class, exp, _ := classifyErr(tc.err); class != tc.class || !exp {
			t.Errorf("%s: got (%q, %v), want %q", name, class, exp, tc.class)
		}
	}
}

func TestStatusOnCorruptStateShowsReasonAndKeepsFile(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".instancez", "telemetry.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	body := `{"anonymous_id":"x","disabled":true,}`
	_ = os.WriteFile(p, []byte(body), 0o600)
	_, events, code := runCLI(t, home, debugEnv, "validate", "--config", "/nonexistent.yaml")
	if code == 0 || len(events) != 0 {
		t.Errorf("code=%d events=%v", code, events)
	}
	if b, _ := os.ReadFile(p); string(b) != body {
		t.Errorf("state rewritten: %s", b)
	}
}

func TestReadOnlyCommandsLeaveNoState(t *testing.T) {
	for _, args := range [][]string{{"telemetry", "status"}, {"completion", "bash"}, {"help"}, {"__complete", "va"}} {
		home := t.TempDir()
		_, events, _ := runCLI(t, home, debugEnv, args...)
		if len(events) != 0 {
			t.Errorf("%v sent %v", args, events)
		}
		if _, err := os.Stat(filepath.Join(home, ".instancez")); !os.IsNotExist(err) {
			t.Errorf("%v wrote state: %v", args, err)
		}
	}
}

func TestDevRuntimeErrorIsDedupedAndCapped(t *testing.T) {
	buf := debugTele(t)
	for i := 0; i < 3; i++ {
		captureDevRuntimeError(fmt.Errorf("same failure"))
	}
	for i := 0; i < 30; i++ {
		captureDevRuntimeError(fmt.Errorf("failure %d", i))
	}
	n := 0
	for _, ev := range events(buf) {
		if ev.Event == "dev_runtime_error" {
			n++
		}
	}
	if n != maxDevRuntimeErrors {
		t.Errorf("sent %d events, want %d", n, maxDevRuntimeErrors)
	}
}

func resetDevRuntimeErrors() {
	devErrs.Lock()
	devErrs.seen = map[string]bool{}
	devErrs.Unlock()
}

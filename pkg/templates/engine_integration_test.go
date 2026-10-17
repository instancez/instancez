//go:build integration

package templates

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestEngineBoot proves every template against the real instancez engine: it
// boots `inz dev` on a throwaway database, so migrations, RLS policies, rpc
// bodies and function loading all run for real. Opt in with a superuser DSN:
//
//	docker run -d --rm -p 55432:5432 -e POSTGRES_PASSWORD=pw postgres:16
//	INSTANCEZ_TEST_PG_DSN=postgres://postgres:pw@localhost:55432/postgres go test -tags=integration ./pkg/templates -run EngineBoot
func TestEngineBoot(t *testing.T) {
	dsn := os.Getenv("INSTANCEZ_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("INSTANCEZ_TEST_PG_DSN not set")
	}
	inz := buildInz(t)
	c, err := Load(embedded, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("TEMPLATES_ONLY")
	sem := make(chan struct{}, 1) // parallel boots race on shared catalog DDL ("tuple concurrently updated")
	var wg sync.WaitGroup
	for _, tpl := range c.List() {
		if only != "" && !strings.Contains(","+only+",", ","+tpl.ID+",") {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			t.Run(tpl.ID, func(t *testing.T) { bootTemplate(t, inz, dsn, tpl) })
		}()
	}
	wg.Wait()
}

func bootTemplate(t *testing.T, inz, adminDSN string, tpl Template) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	dbName := "tpl_" + strings.ReplaceAll(tpl.ID, "-", "_")
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	_, _ = conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName)) }()

	dir := t.TempDir()
	cfg := tpl.Bundle().LocalYAML()
	write(t, filepath.Join(dir, "instancez.yaml"), cfg)
	for p, src := range tpl.Files {
		write(t, filepath.Join(dir, p), src)
	}
	if _, ok := tpl.Files[packageJSON]; ok {
		npm := exec.Command("npm", "install", "--no-audit", "--no-fund", "--silent")
		npm.Dir = filepath.Join(dir, "functions")
		if out, err := npm.CombinedOutput(); err != nil {
			t.Fatalf("npm install: %v\n%s", err, out)
		}
	}

	u, _ := url.Parse(adminDSN)
	u.Path = "/" + dbName
	port := freePort(t)
	var logBuf syncBuffer
	cmd := exec.CommandContext(ctx, inz, "dev", "--no-watch", "--port", fmt.Sprint(port), "--dashboard", "disabled")
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(), "INSTANCEZ_DATABASE_URL="+u.String())
	cmd.Stdout, cmd.Stderr = &logBuf, &logBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() { cancel(); <-exited }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitReady(base, exited); err != nil {
		t.Fatalf("engine did not boot: %v\n%s", err, logBuf.Tail(40))
	}
	anon := readEnvFile(t, filepath.Join(dir, ".development.env"))["INSTANCEZ_PUBLISHABLE_KEY"]
	if anon == "" {
		t.Fatalf("no publishable key generated\n%s", logBuf.Tail(20))
	}
	jwt := signUp(t, base, anon)

	for _, table := range tpl.Includes.Tables {
		for name, bearer := range map[string]string{"anon": anon, "authenticated": jwt} {
			status, body := httpDo(t, "GET", base+"/rest/v1/"+table+"?limit=1", anon, bearer, nil)
			if status != 200 {
				t.Errorf("GET %s as %s = %d: %s", table, name, status, body)
			}
		}
	}
	for _, fn := range tpl.Includes.Functions {
		status, body := httpDo(t, "POST", base+"/functions/v1/"+fn, anon, anon, []byte("{}"))
		if status >= 500 && (status != 503 || !strings.Contains(body, "INSTANCEZ_ENV_")) {
			t.Errorf("function %s anon = %d: %s", fn, status, body)
		}
	}
	// A recursion or SQL error inside a policy surfaces as a logged engine error.
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, "level=ERROR") {
			t.Errorf("engine logged: %s", line)
		}
	}
}

func signUp(t *testing.T, base, anon string) string {
	body, _ := json.Marshal(map[string]string{"email": "tester@example.com", "password": "Tester-pass-123"})
	status, resp := httpDo(t, "POST", base+"/auth/v1/signup", anon, anon, body)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if status != 200 || json.Unmarshal([]byte(resp), &out) != nil || out.AccessToken == "" {
		t.Fatalf("signup = %d: %s", status, resp)
	}
	return out.AccessToken
}

func httpDo(t *testing.T, method, u, apikey, bearer string, body []byte) (int, string) {
	req, _ := http.NewRequest(method, u, bytes.NewReader(body))
	req.Header.Set("apikey", apikey)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func waitReady(base string, exited <-chan struct{}) error {
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return fmt.Errorf("process exited")
		default:
		}
		if res, err := http.Get(base + "/rest/v1/"); err == nil {
			_ = res.Body.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout")
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func write(t *testing.T, path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readEnvFile(t *testing.T, path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

// cleanEnv drops any INSTANCEZ_ENV_ secrets so the engine boots unconfigured, as a fresh project does.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "INSTANCEZ_ENV_") && !strings.HasPrefix(kv, "INSTANCEZ_DATABASE_URL=") {
			env = append(env, kv)
		}
	}
	return env
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
func (s *syncBuffer) Tail(n int) string {
	lines := strings.Split(s.String(), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func buildInz(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "inz")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/inz")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build inz: %v\n%s", err, out)
	}
	return bin
}

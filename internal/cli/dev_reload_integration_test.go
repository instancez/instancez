//go:build integration

package cli

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const reloadBaseYAML = "version: 1\nproject:\n  name: reload\n"

const reloadTableYAML = reloadBaseYAML + `tables:
  notes:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: body
        type: text
`

func reloadTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func restStatus(port int, table string) int {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/rest/v1/%s", port, table), nil)
	req.Header.Set("apikey", "inz_secret_reloadtest")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

func waitStatus(port int, table string, want int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if restStatus(port, table) == want {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// `inz dev` must hot-reload a config saved the way most editors (and Claude
// Code's Edit tool) save: write a temp file, then rename it over the original.
func TestDevHotReload_AtomicRenameSave(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "inz")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/inz").CombinedOutput(); err != nil {
		t.Fatalf("build inz: %v\n%s", err, out)
	}

	proj := t.TempDir()
	cfgPath := filepath.Join(proj, "instancez.yaml")
	if err := os.WriteFile(cfgPath, []byte(reloadBaseYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	port := reloadTestPort(t)
	var logs bytes.Buffer
	cmd := exec.Command(bin, "dev", "--embedded-pg", "--port", fmt.Sprint(port))
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "INSTANCEZ_SECRET_KEY=inz_secret_reloadtest", "INSTANCEZ_PUBLISHABLE_KEY=inz_publishable_reloadtest")
	cmd.Stdout, cmd.Stderr = &logs, &logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
	})

	// Any non-zero status means the server is up (unknown table is 404).
	deadline := time.Now().Add(3 * time.Minute)
	for restStatus(port, "notes") == 0 {
		if time.Now().After(deadline) {
			if strings.Contains(logs.String(), "embedded") && isEmbeddedPGEnvLimitation(fmt.Errorf("%s", logs.String())) {
				t.Skip("embedded Postgres unavailable in this environment")
			}
			t.Fatalf("server did not start:\n%s", logs.String())
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got := restStatus(port, "notes"); got != 404 {
		t.Fatalf("notes before reload = %d, want 404", got)
	}

	tmp := filepath.Join(proj, ".instancez.yaml.tmp")
	if err := os.WriteFile(tmp, []byte(reloadTableYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		t.Fatal(err)
	}
	if !waitStatus(port, "notes", 200, 20*time.Second) {
		t.Fatalf("rename-style save did not reload config (notes = %d)\n%s", restStatus(port, "notes"), logs.String())
	}

	// An invalid save keeps the last good config serving.
	atomicWrite := func(content string) {
		if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, cfgPath); err != nil {
			t.Fatal(err)
		}
	}
	atomicWrite("version: 1\ntables: [not: valid\n")
	time.Sleep(2 * time.Second)
	if got := restStatus(port, "notes"); got != 200 {
		t.Fatalf("notes after invalid save = %d, want 200 (last good config kept)", got)
	}

	// The watch survives the failed save: a following good save still applies.
	atomicWrite(reloadBaseYAML)
	// notes dropped from config; dev mode allows destructive drops.
	if !waitStatus(port, "notes", 404, 20*time.Second) {
		t.Fatalf("save after invalid save did not reload (notes = %d)\n%s", restStatus(port, "notes"), logs.String())
	}
}

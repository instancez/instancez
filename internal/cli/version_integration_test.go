//go:build integration

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoBuildVersionIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(strings.TrimSpace(string(gomod)))
	rev, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	bin := filepath.Join(t.TempDir(), "inz")
	build := exec.Command("go", "build", "-o", bin, "./cmd/inz")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	info, _ := exec.Command("go", "version", "-m", bin).Output()
	if !strings.Contains(string(info), "vcs.revision") {
		t.Skip("binary has no VCS stamp")
	}
	// nosemgrep -- runs the binary the test just built
	cmd := exec.Command(bin, "--version")
	cmd.Env = append(os.Environ(), "INSTANCEZ_TELEMETRY_DISABLED=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "vdev (unknown)") {
		t.Errorf("version not resolved: %q", got)
	}
	if want := strings.TrimSpace(string(rev))[:7]; !strings.Contains(got, want) {
		t.Errorf("version %q missing commit %s", got, want)
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const badYAML = `tables:
  posts:
    rls_enabled: true
    fields:
      - {name: id, type: uuid, primary_key: true}
    rls:
      - operations: [insert]
        with_check: "true"
`

// cleanYAML has no policy problems; the default unverified-signup finding is the only one left.
const cleanYAML = `tables:
  posts:
    rls_enabled: true
    fields:
      - {name: id, type: uuid, primary_key: true}
      - {name: user_id, type: uuid}
    rls:
      - operations: [select]
        using: "auth.uid() = user_id"
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func runVet(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"vet"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestVetExitSemantics(t *testing.T) {
	bad := writeTemp(t, badYAML)
	clean := writeTemp(t, cleanYAML)
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"bad default high", []string{"--config", bad}, true},
		{"bad fail-on none", []string{"--config", bad, "--fail-on", "none"}, false},
		{"bad fail-on critical", []string{"--config", bad, "--fail-on", "critical"}, true},
		{"bad ignore rule", []string{"--config", bad, "--ignore", "policy-open-write"}, false},
		{"bad ignore unknown id", []string{"--config", bad, "--ignore", "nope"}, true},
		{"clean default", []string{"--config", clean}, false},
		{"clean fail-on info sees default finding", []string{"--config", clean, "--fail-on", "info"}, true},
		{"clean fail-on info ignoring all", []string{"--config", clean, "--fail-on", "info", "--ignore", "signup-unverified-email"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := runVet(t, c.args...)
			if c.wantErr {
				require.ErrorIs(t, err, errReported)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestVetPlainErrors(t *testing.T) {
	cases := map[string][]string{
		"parse error":      {"--config", writeTemp(t, "tables: [unclosed")},
		"missing file":     {"--config", filepath.Join(t.TempDir(), "none.yaml")},
		"bad fail-on":      {"--config", writeTemp(t, cleanYAML), "--fail-on", "severe"},
		"remote config":    {"--config", "s3://b/k"},
		"positional extra": {"--config", writeTemp(t, cleanYAML), "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runVet(t, args...)
			require.Error(t, err)
			assert.NotErrorIs(t, err, errReported)
		})
	}
}

func TestVetJSON(t *testing.T) {
	out, err := runVet(t, "--config", writeTemp(t, badYAML), "--json", "--ignore", "signup-unverified-email")
	require.ErrorIs(t, err, errReported)
	var r struct {
		Findings []map[string]any `json:"findings"`
		Counts   map[string]int   `json:"counts"`
		Checks   struct{ Total, Passed int }
	}
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Greater(t, r.Checks.Total, 0)
	assert.Equal(t, r.Checks.Total-1, r.Checks.Passed)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, "policy-open-write", r.Findings[0]["rule"])
	assert.Equal(t, "critical", r.Findings[0]["severity"])
	assert.Equal(t, 1, r.Counts["critical"])
	assert.Contains(t, r.Counts, "info")
}

func TestVetJSONEmptyFindingsIsArray(t *testing.T) {
	out, err := runVet(t, "--config", writeTemp(t, cleanYAML), "--json", "--ignore", "signup-unverified-email")
	require.NoError(t, err)
	assert.Contains(t, out, `"findings": []`)
}

func TestVetPrettyOutputPlain(t *testing.T) {
	out, _ := runVet(t, "--config", writeTemp(t, badYAML))
	assert.NotContains(t, out, "\x1b[")
	assert.Contains(t, out, "policy-open-write")
	assert.Contains(t, out, "failed")
	assert.Regexp(t, `\d+ of \d+ checks passed`, out)
}

func TestVetEnvBinding(t *testing.T) {
	bad := writeTemp(t, badYAML)
	t.Run("INSTANCEZ_FAIL_ON none", func(t *testing.T) {
		t.Setenv("INSTANCEZ_FAIL_ON", "none")
		_, err := runVet(t, "--config", bad)
		require.NoError(t, err)
	})
	t.Run("INSTANCEZ_IGNORE comma list", func(t *testing.T) {
		t.Setenv("INSTANCEZ_IGNORE", "policy-open-write,signup-unverified-email")
		_, err := runVet(t, "--config", bad)
		require.NoError(t, err)
	})
	t.Run("INSTANCEZ_JSON yes", func(t *testing.T) {
		t.Setenv("INSTANCEZ_JSON", "yes")
		out, _ := runVet(t, "--config", bad)
		assert.True(t, json.Valid([]byte(out)), out)
	})
	t.Run("flag beats env", func(t *testing.T) {
		t.Setenv("INSTANCEZ_FAIL_ON", "none")
		_, err := runVet(t, "--config", bad, "--fail-on", "high")
		require.ErrorIs(t, err, errReported)
	})
}

func TestVetShippedExamples(t *testing.T) {
	for _, p := range []string{
		"../../instancez.yaml",
		"../../docs/examples/gearstore/instancez.yaml",
		"../../dashboard/e2e/fixtures/instancez.yaml",
	} {
		t.Run(filepath.Base(filepath.Dir(p)), func(t *testing.T) {
			out, err := runVet(t, "--config", p, "--json", "--fail-on", "none")
			require.NoError(t, err)
			var r struct {
				Findings []map[string]any `json:"findings"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &r), out)
			assert.NotNil(t, r.Findings)
		})
	}
}

func TestVetBinary(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain unavailable")
	}
	bin := filepath.Join(t.TempDir(), "inz")
	build := exec.Command(goBin, "build", "-o", bin, "../../cmd/inz")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("go build failed: %v\n%s", err, out)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, append([]string{"vet"}, args...)...)
		out, err := cmd.Output()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), ee.ExitCode()
		}
		require.NoError(t, err)
		return string(out), 0
	}

	out, code := run("--config", writeTemp(t, badYAML), "--json")
	assert.Equal(t, 1, code)
	assert.Contains(t, out, `"policy-open-write"`)

	out, code = run("--config", writeTemp(t, cleanYAML), "--ignore", "signup-unverified-email")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "No security findings")
	assert.False(t, strings.Contains(out, "\x1b["), "piped output must be plain")
}

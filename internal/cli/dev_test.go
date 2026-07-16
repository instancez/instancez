package cli

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

func TestParseDevFlagsDefaults(t *testing.T) {
	got, err := parseDevFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != true {
		t.Fatalf("default watch in dev should be true")
	}
	if got.watchInterval != 60*time.Second {
		t.Fatalf("default watch interval should be 60s")
	}
	if got.dashboard != DashboardReadwrite {
		t.Fatalf("default dashboard in dev should be readwrite")
	}
	if got.dbSrc != DevDBSourceDSN {
		t.Fatalf("dbSrc = %v, want DevDBSourceDSN", got.dbSrc)
	}
}

func TestParseDevFlagsOverrides(t *testing.T) {
	got, err := parseDevFlags(
		[]string{"--no-watch", "--dashboard", "disabled"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != false {
		t.Fatalf("--no-watch should turn watch off")
	}
	if got.dashboard != DashboardDisabled {
		t.Fatalf("--dashboard disabled should be honored in dev")
	}
}

// TestParseDevFlagsUseDSNDeprecated verifies that --use-dsn is still accepted
// (hidden/deprecated no-op) and resolves to DevDBSourceDSN.
func TestParseDevFlagsUseDSNDeprecated(t *testing.T) {
	got, err := parseDevFlags([]string{"--use-dsn"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("--use-dsn should still be accepted (deprecated no-op): %v", err)
	}
	if got.dbSrc != DevDBSourceDSN {
		t.Fatalf("dbSrc = %v, want DevDBSourceDSN", got.dbSrc)
	}
}

// TestParseDevFlagsVerboseFromEnv verifies the standardized INSTANCEZ_VERBOSE
// env var binds to the --verbose flag (it had no env binding before).
func TestParseDevFlagsVerboseFromEnv(t *testing.T) {
	got, err := parseDevFlags([]string{}, func(k string) string {
		return map[string]string{"INSTANCEZ_VERBOSE": "true"}[k]
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.verbose {
		t.Fatal("INSTANCEZ_VERBOSE=true should set verbose")
	}
}

// TestParseDevFlagsRemovedFlagsUnknown verifies that the removed --use-docker
// and --use-cloud-ephemeral flags are now unknown and cause a parse error.
func TestParseDevFlagsRemovedFlagsUnknown(t *testing.T) {
	for _, flag := range []string{"--use-docker", "--use-cloud-ephemeral", "--use-cloud"} {
		_, err := parseDevFlags([]string{flag}, func(string) string { return "" })
		if err == nil {
			t.Errorf("%s: expected parse error for removed flag, got nil", flag)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: error %q should mention 'unknown flag'", flag, err.Error())
		}
	}
}

// TestParseDevFlagsEmbeddedPG verifies --embedded-pg sets DevDBSourceEmbedded.
func TestParseDevFlagsEmbeddedPG(t *testing.T) {
	got, err := parseDevFlags([]string{"--embedded-pg"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dbSrc != DevDBSourceEmbedded {
		t.Fatalf("dbSrc = %v, want DevDBSourceEmbedded", got.dbSrc)
	}
	if got.resetPG {
		t.Fatal("resetPG should be false when --reset-pg not passed")
	}
}

// TestParseDevFlagsResetPGRequiresEmbedded verifies --reset-pg without --embedded-pg is an error.
func TestParseDevFlagsResetPGRequiresEmbedded(t *testing.T) {
	_, err := parseDevFlags([]string{"--reset-pg"}, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error for --reset-pg without --embedded-pg")
	}
	if !strings.Contains(err.Error(), "--reset-pg requires --embedded-pg") {
		t.Fatalf("error %q should mention --reset-pg requires --embedded-pg", err.Error())
	}
}

// TestParseDevFlagsEmbeddedPGWithReset verifies --embedded-pg --reset-pg is accepted.
func TestParseDevFlagsEmbeddedPGWithReset(t *testing.T) {
	got, err := parseDevFlags([]string{"--embedded-pg", "--reset-pg"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dbSrc != DevDBSourceEmbedded {
		t.Fatalf("dbSrc = %v, want DevDBSourceEmbedded", got.dbSrc)
	}
	if !got.resetPG {
		t.Fatal("resetPG should be true when --reset-pg is passed")
	}
}

// TestParseDevFlagsPGDataDir verifies pgDataDir is derived from configPath.
func TestParseDevFlagsPGDataDir(t *testing.T) {
	got, err := parseDevFlags(
		[]string{"--embedded-pg", "--config", "/tmp/myproject/instancez.yaml"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "/tmp/myproject/pgdata"
	if got.pgDataDir != want {
		t.Fatalf("pgDataDir = %q, want %q", got.pgDataDir, want)
	}
}

// TestParseDevFlagsPGDataDirBareConfig verifies pgDataDir with the default
// bare config filename ("instancez.yaml") resolves to "pgdata" (no leading
// "./" — filepath.Join(".", "pgdata") returns "pgdata").
func TestParseDevFlagsPGDataDirBareConfig(t *testing.T) {
	got, err := parseDevFlags(
		[]string{"--embedded-pg"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "pgdata"
	if got.pgDataDir != want {
		t.Fatalf("pgDataDir = %q, want %q", got.pgDataDir, want)
	}
}

// TestParseDevFlagsPGDataDirEmptyWithoutEmbedded verifies pgDataDir is empty
// when --embedded-pg is not passed, so callers need not guard on dbSrc before
// using pgDataDir.
func TestParseDevFlagsPGDataDirEmptyWithoutEmbedded(t *testing.T) {
	got, err := parseDevFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.pgDataDir != "" {
		t.Fatalf("pgDataDir = %q, want empty string when --embedded-pg not set", got.pgDataDir)
	}
}

// TestIsFunctionRuntimeShim guards the dev functions-watcher against reacting to
// its own artifact: each runtime reload writes a fresh ".inz-worker-<rand>.mjs"
// into functions/, so treating that write as a source change loops forever.
func TestIsFunctionRuntimeShim(t *testing.T) {
	shims := []string{".inz-worker-abc123.mjs", ".inz-worker-0.mjs"}
	for _, n := range shims {
		if !isFunctionRuntimeShim(n) {
			t.Errorf("isFunctionRuntimeShim(%q) = false, want true", n)
		}
	}
	sources := []string{"greet.js", "todos.ts", "package.json", "worker.mjs", ".env"}
	for _, n := range sources {
		if isFunctionRuntimeShim(n) {
			t.Errorf("isFunctionRuntimeShim(%q) = true, want false", n)
		}
	}
}

// TestWatchFunctionsDir covers the two dev-watcher guarantees the dashboard's
// runtime "create function" flow depends on: a source write triggers a reload
// carrying the LIVE config (not the boot snapshot, so a just-added function is
// seen), and the runtime's own ".inz-worker-*.mjs" shim writes are ignored (so
// each reload doesn't trigger the next).
func TestWatchFunctionsDir(t *testing.T) {
	dir := t.TempDir()

	// live config the watcher should reload against: starts empty, gains
	// "greet" the way a dashboard PUT /config would before the .js write.
	var live atomic.Pointer[domain.Config]
	live.Store(&domain.Config{})

	reloads := make(chan *domain.Config, 16)
	reload := func(c *domain.Config) { reloads <- c }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go watchFunctionsDir(ctx, dir, func() *domain.Config { return live.Load() }, reload, logger)

	// A shim write must NOT cause a reload.
	writeFile(t, filepath.Join(dir, ".inz-worker-deadbeef.mjs"), "// shim")
	select {
	case <-reloads:
		t.Fatal("shim write triggered a reload; the watcher must ignore .inz-worker-*.mjs")
	case <-time.After(600 * time.Millisecond):
	}

	// Simulate the create flow: live config now declares greet, then the .js
	// file lands. The reload must see greet.
	live.Store(&domain.Config{Functions: map[string]domain.CodeFunction{
		"greet": {Runtime: "node", File: "functions/greet.js"},
	}})
	writeFile(t, filepath.Join(dir, "greet.js"), "export default () => {}")

	select {
	case got := <-reloads:
		if _, ok := got.Functions["greet"]; !ok {
			t.Fatalf("reload config missing greet; got %d functions (stale boot snapshot?)", len(got.Functions))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("source write did not trigger a reload")
	}
}

func TestDevBannerListsEnabledResources(t *testing.T) {
	b := devBanner{
		functions: 2,
		cfg: &domain.Config{
			Tables:  map[string]domain.Table{"a": {}, "b": {}, "c": {}},
			Storage: map[string]domain.Bucket{"avatars": {}, "docs": {}},
			RPC:     map[string]domain.Function{"hello": {}},
			Auth:    &domain.Auth{},
		},
	}
	var buf strings.Builder
	b.write(&buf)
	out := buf.String()

	for _, want := range []string{
		"Tables           3",
		"Storage buckets  2",
		"RPC functions    1",
		"Auth             enabled",
		"✓ 2 function(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q\n%s", want, out)
		}
	}
}

func TestDevBannerAuthDisabled(t *testing.T) {
	b := devBanner{functions: -1, cfg: &domain.Config{}}
	var buf strings.Builder
	b.write(&buf)
	if !strings.Contains(buf.String(), "Auth             disabled") {
		t.Errorf("expected Auth disabled\n%s", buf.String())
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunBundleNoOutput(t *testing.T) {
	dir := writeBundleFixture(t)
	configPath := filepath.Join(dir, "instancez.yaml")
	t.Chdir(t.TempDir())
	tmpGlob := filepath.Join(os.TempDir(), "inz-functions-bundle-*.tar.gz")
	before, _ := filepath.Glob(tmpGlob)

	cmd := newBundleCmd()
	cmd.SetArgs([]string{"--config", configPath})
	require.NoError(t, cmd.ExecuteContext(context.Background()))

	assert.Contains(t, tarEntries(t, "bundle.tar.gz"), "instancez.yaml")
	after, _ := filepath.Glob(tmpGlob)
	assert.Len(t, after, len(before), "temp bundle must be removed")
}

func TestRunBundleLocalOutput(t *testing.T) {
	dir := writeBundleFixture(t)
	configPath := filepath.Join(dir, "instancez.yaml")
	dest := filepath.Join(t.TempDir(), "out.tar.gz")

	err := runBundle(context.Background(), configPath, dest)
	require.NoError(t, err)

	fi, err := os.Stat(dest)
	require.NoError(t, err, "output file must exist")
	assert.Greater(t, fi.Size(), int64(0), "output file must be non-empty")

	// Must be a valid tar that contains instancez.yaml.
	names := tarEntries(t, dest)
	assert.Contains(t, names, "instancez.yaml")
	assert.Contains(t, names, "manifest.json")
}

func TestRunBundleMissingFunctionFile(t *testing.T) {
	dir := t.TempDir()
	yaml := "version: 1\nfunctions:\n  hello:\n    runtime: node\n    file: functions/missing.js\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte(yaml), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "functions"), 0o755))

	err := runBundle(context.Background(), filepath.Join(dir, "instancez.yaml"), "")
	require.Error(t, err, "missing function file must fail")
}

func TestRunBundleRejectsS3Config(t *testing.T) {
	err := runBundle(context.Background(), "s3://bucket/instancez.yaml", "")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "local") || strings.Contains(err.Error(), "remote"),
		"error should explain the restriction")
}

func TestRunBundleDoesNotPackItsOwnOutput(t *testing.T) {
	dir := writeBundleFixture(t)
	configPath := filepath.Join(dir, "instancez.yaml")
	out := filepath.Join(dir, "functions", "bundle.tar.gz")
	require.NoError(t, runBundle(context.Background(), configPath, out))
	first, err := os.Stat(out)
	require.NoError(t, err)
	require.NoError(t, runBundle(context.Background(), configPath, out))
	assert.NotContains(t, tarEntries(t, out), "functions/bundle.tar.gz")
	second, err := os.Stat(out)
	require.NoError(t, err)
	assert.InDelta(t, first.Size(), second.Size(), 64, "bundle must not grow between runs")
}

func TestRunBundleRejectsEmptyOutput(t *testing.T) {
	dir := writeBundleFixture(t)
	err := runBundle(context.Background(), filepath.Join(dir, "instancez.yaml"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--output needs a path or s3://bucket/key")
}

func TestRunBundleOutputIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("checks Unix permission bits")
	}
	dir := writeBundleFixture(t)
	configPath := filepath.Join(dir, "instancez.yaml")
	for name, pre := range map[string]bool{"new file": false, "existing 0644 file": true} {
		dest := filepath.Join(t.TempDir(), "out.tar.gz")
		if pre {
			require.NoError(t, os.WriteFile(dest, bytes.Repeat([]byte("old"), 1<<20), 0o644))
			require.NoError(t, os.Chmod(dest, 0o644))
		}
		require.NoError(t, runBundle(context.Background(), configPath, dest), name)
		fi, err := os.Stat(dest)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), name)
		assert.Contains(t, tarEntries(t, dest), "instancez.yaml", name+": stale bytes must be truncated")
	}
}

func TestRunBundleDoesNotFollowOutputSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks")
	}
	dir := writeBundleFixture(t)
	victim := filepath.Join(t.TempDir(), "victim.txt")
	require.NoError(t, os.WriteFile(victim, []byte("precious"), 0o600))
	dest := filepath.Join(t.TempDir(), "bundle.tar.gz")
	require.NoError(t, os.Symlink(victim, dest))

	require.NoError(t, runBundle(context.Background(), filepath.Join(dir, "instancez.yaml"), dest))

	got, err := os.ReadFile(victim) // nosemgrep -- path is in t.TempDir
	require.NoError(t, err)
	assert.Equal(t, "precious", string(got), "the symlink target must not be overwritten")
	fi, err := os.Lstat(dest)
	require.NoError(t, err)
	assert.True(t, fi.Mode().IsRegular(), "the symlink is replaced by the bundle")
	assert.Contains(t, tarEntries(t, dest), "instancez.yaml")
}

func TestRunBundleLeavesNoTempFileOnFailure(t *testing.T) {
	dir := writeBundleFixture(t)
	outDir := t.TempDir()
	dest := filepath.Join(outDir, "sub", "bundle.tar.gz")
	require.Error(t, runBundle(context.Background(), filepath.Join(dir, "instancez.yaml"), dest))
	entries, _ := os.ReadDir(outDir)
	assert.Empty(t, entries)
}

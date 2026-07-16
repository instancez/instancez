package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunBundleNoOutput(t *testing.T) {
	dir := writeBundleFixture(t)
	configPath := filepath.Join(dir, "instancez.yaml")

	err := runBundle(context.Background(), configPath, "")
	require.NoError(t, err)
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

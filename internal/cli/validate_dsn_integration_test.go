//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/testutil/dbboot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const badColumnYAML = `version: 1
auth: {}
tables:
  todos:
    rls_enabled: true
    fields:
      - {name: id, type: bigserial, primary_key: true}
    rls:
      - operations: [select]
        using: nonexist = auth.uid()
`

func writeDSNCfg(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "instancez.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o644))
	return p
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func superDSN(t *testing.T) string {
	t.Helper()
	_, _, _, dsn := dbboot.StartContainerWithRawAuth(t)
	return dsn
}

func TestValidateUseDSN_DryRunFails(t *testing.T) {
	dsn := superDSN(t)
	path := writeDSNCfg(t, badColumnYAML)
	var errOut bytes.Buffer
	var err error
	captureStdout(t, func() { err = runValidate(context.Background(), &errOut, path, false, dsn, true) })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dry run")
	assert.Contains(t, err.Error(), `column "nonexist" does not exist`)
}

func TestValidateUseDSN_PlainStaysPlanOnly(t *testing.T) {
	dsn := superDSN(t)
	path := writeDSNCfg(t, badColumnYAML)
	var err error
	out := captureStdout(t, func() { err = runValidate(context.Background(), io.Discard, path, false, dsn, false) })
	require.NoError(t, err, "plain --use-dsn must not run DDL")
	assert.NotContains(t, out, "Dry run")
}

func TestValidateUseDSN_JSON(t *testing.T) {
	dsn := superDSN(t)
	path := writeDSNCfg(t, badColumnYAML)
	good := writeDSNCfg(t, "version: 1\ntables:\n  t:\n    rls_enabled: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n")
	var err error
	out := captureStdout(t, func() { err = runValidate(context.Background(), io.Discard, good, true, dsn, true) })
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "ok", got["dry_run"])

	out = captureStdout(t, func() { err = runValidate(context.Background(), io.Discard, path, true, dsn, true) })
	require.Error(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Contains(t, got["dry_run"], "nonexist")

	out = captureStdout(t, func() { err = runValidate(context.Background(), io.Discard, path, true, dsn, false) })
	require.NoError(t, err)
	got = nil
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.NotContains(t, got, "dry_run")
}

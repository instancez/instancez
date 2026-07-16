package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredentialsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Empty load returns ErrNoCredentials.
	_, err := Load()
	assert.ErrorIs(t, err, ErrNoCredentials)

	// Save then Load returns the same value.
	saved := Credentials{PAT: "instancez_pat_abc123", Email: "me@example.com"}
	assert.NoError(t, Save(saved))

	loaded, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, saved, loaded)

	// File mode is 0600.
	info, err := os.Stat(filepath.Join(dir, ".instancez", "credentials"))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Delete removes the file.
	assert.NoError(t, Delete())
	_, err = Load()
	assert.ErrorIs(t, err, ErrNoCredentials)
}

func TestLoadPrefersEnvPATOverFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	require.NoError(t, Save(Credentials{PAT: "file-pat", Email: "file@example.com"}))

	t.Setenv("INSTANCEZ_CLOUD_PAT", "env-pat")
	creds, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "env-pat", creds.PAT)
	assert.Empty(t, creds.Email, "env-var auth has no informational email")
}

func TestLoadEnvPATWithNoCredentialsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("INSTANCEZ_CLOUD_PAT", "env-pat-only")

	creds, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "env-pat-only", creds.PAT)
}

func TestLoadFallsBackToFileWhenEnvUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("INSTANCEZ_CLOUD_PAT", "")
	require.NoError(t, Save(Credentials{PAT: "file-pat", Email: "file@example.com"}))

	creds, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "file-pat", creds.PAT)
	assert.Equal(t, "file@example.com", creds.Email)
}

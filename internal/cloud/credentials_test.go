package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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

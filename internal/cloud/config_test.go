package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIURLDefault(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "")
	assert.Equal(t, defaultCloudAPI, APIURL())
}

func TestAPIURLFromEnv(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "https://staging.cloud.example.com")
	assert.Equal(t, "https://staging.cloud.example.com", APIURL())
}

func TestAPIURLTrimsTrailingSlash(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "https://x.example.com/")
	assert.Equal(t, "https://x.example.com", APIURL())
}

func TestAPIURLFromConfigPrefersYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte(`
version: 1
project:
  cloud:
    api_url: https://project-pinned.example.com
`), 0o644))

	t.Setenv("INSTANCEZ_CLOUD_API", "https://env.example.com")
	got, err := APIURLFromConfig(yamlPath)
	assert.NoError(t, err)
	assert.Equal(t, "https://project-pinned.example.com", got)
}

func TestAPIURLFromConfigFallsBackToEnv(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte(`
version: 1
project:
  name: x
`), 0o644))

	t.Setenv("INSTANCEZ_CLOUD_API", "https://env.example.com")
	got, err := APIURLFromConfig(yamlPath)
	assert.NoError(t, err)
	assert.Equal(t, "https://env.example.com", got)
}

func TestAPIURLFromConfigMissingFile(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "https://env.example.com")
	// Missing file → fall back to env, no error.
	got, err := APIURLFromConfig("/no/such/file.yaml")
	assert.NoError(t, err)
	assert.Equal(t, "https://env.example.com", got)
}

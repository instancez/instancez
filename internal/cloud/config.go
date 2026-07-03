package cloud

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// defaultCloudAPI is the Instancez Cloud API endpoint. Can be overridden by
// INSTANCEZ_CLOUD_API env var or project.cloud.api_url in instancez.yaml.
const defaultCloudAPI = "https://my.instancez.ai/api"

// APIURL returns the base URL for the Instancez Cloud API, considering only
// the environment variable. Used by commands that run without a project
// context (login, logout).
func APIURL() string {
	if v := os.Getenv("INSTANCEZ_CLOUD_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultCloudAPI
}

// APIURLFromConfig returns the base URL with project-level override applied.
// Reads project.cloud.api_url from the given instancez.yaml; falls back to
// APIURL() if the file is missing or has no api_url field.
//
// Returns an error only on malformed YAML — a missing file or absent field
// is fine and yields the env/default value.
func APIURLFromConfig(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return APIURL(), nil
		}
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}
	pinned, err := ReadAPIURL(data)
	if err != nil {
		return "", err
	}
	if pinned != "" {
		return strings.TrimRight(pinned, "/"), nil
	}
	return APIURL(), nil
}

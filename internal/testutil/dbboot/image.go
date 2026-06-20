package dbboot

import "os"

const (
	// pgImageEnv lets CI pick the Postgres image so the integration suite can
	// run as a major-version matrix. Unset locally, callers get defaultPGImage.
	pgImageEnv = "INSTANCEZ_TEST_PG_IMAGE"

	defaultPGImage = "postgres:16-alpine"
)

// PostgresImage is the testcontainer image every integration test should boot.
// It reads INSTANCEZ_TEST_PG_IMAGE so a CI matrix can fan the same suite across
// Postgres majors; with the var unset it returns the pinned default. Tests that
// must pin a specific version still pass an explicit image override.
func PostgresImage() string {
	if img := os.Getenv(pgImageEnv); img != "" {
		return img
	}
	return defaultPGImage
}

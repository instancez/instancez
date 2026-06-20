package dbboot

import "testing"

func TestPostgresImage_DefaultsToPinnedVersion(t *testing.T) {
	t.Setenv(pgImageEnv, "")
	if got := PostgresImage(); got != defaultPGImage {
		t.Fatalf("PostgresImage() = %q, want default %q", got, defaultPGImage)
	}
}

func TestPostgresImage_HonorsEnvOverride(t *testing.T) {
	t.Setenv(pgImageEnv, "postgres:17-alpine")
	if got := PostgresImage(); got != "postgres:17-alpine" {
		t.Fatalf("PostgresImage() = %q, want env override", got)
	}
}

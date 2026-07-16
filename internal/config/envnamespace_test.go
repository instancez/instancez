package config

import (
	"strings"
	"testing"
)

func TestValidateEnvNamespace(t *testing.T) {
	ok := []byte("auth:\n  oauth:\n    google:\n      client_secret: ${INSTANCEZ_ENV_GOOGLE_CLIENT_SECRET}\n")
	if errs := ValidateEnvNamespace(ok); errs != nil {
		t.Fatalf("INSTANCEZ_ENV_ ref must pass, got: %v", errs)
	}
	if errs := ValidateEnvNamespace([]byte("x: ${INSTANCEZ_ENV_FOO:-def}\n")); errs != nil {
		t.Errorf("default form must pass, got: %v", errs)
	}
	for _, bad := range []string{"${GOOGLE_CLIENT_SECRET}", "${INSTANCEZ_OWNER_DATABASE_URL}", "${AWS_SECRET_ACCESS_KEY}"} {
		if errs := ValidateEnvNamespace([]byte("x: \"" + bad + "\"\n")); errs == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}

	// A bare user var is told to adopt the prefix; a platform-injected var is
	// told to remove the reference (not aliased into the user namespace).
	bare := ValidateEnvNamespace([]byte("x: ${GOOGLE_CLIENT_SECRET}\n"))
	if len(bare) != 1 || !strings.Contains(bare[0].Suggestion, "INSTANCEZ_ENV_GOOGLE_CLIENT_SECRET") {
		t.Errorf("bare var should be told to prefix, got: %+v", bare)
	}
	platform := ValidateEnvNamespace([]byte("x: ${INSTANCEZ_OWNER_DATABASE_URL}\n"))
	if len(platform) != 1 || !strings.Contains(platform[0].Suggestion, "platform-injected") {
		t.Errorf("platform var should be told to remove, got: %+v", platform)
	}
}

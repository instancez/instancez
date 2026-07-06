package config

import "testing"

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
}

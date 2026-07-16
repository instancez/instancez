package config

import (
	"fmt"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// ValidateEnvNamespace scans raw YAML (pre-interpolation) for ${VAR} refs and
// rejects any whose name is not in the INSTANCEZ_ENV_ namespace. It runs on
// user-authored config only; it is deliberately not part of config.Validate,
// which also runs at runtime on generator-produced configs that reference
// platform vars.
func ValidateEnvNamespace(raw []byte) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for _, name := range EnvRefs(raw) {
		if strings.HasPrefix(name, instancezEnvPrefix) {
			continue
		}
		// A bare user var just needs the prefix; an INSTANCEZ_/AWS_ name is a
		// platform-injected var that should be dropped, not aliased.
		suggestion := "Rename it to INSTANCEZ_ENV_" + name + " so it lives in the user namespace."
		if strings.HasPrefix(name, "INSTANCEZ_") || strings.HasPrefix(name, "AWS_") {
			suggestion = "This is a platform-injected variable; remove the reference (it cannot be set from config)."
		}
		errs = append(errs, &domain.ValidationError{
			Path:       "${" + name + "}",
			Message:    fmt.Sprintf("environment variable %q may not be referenced in config; only the INSTANCEZ_ENV_ namespace is allowed", name),
			Suggestion: suggestion,
		})
	}
	return errs
}

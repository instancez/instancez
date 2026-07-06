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
		errs = append(errs, &domain.ValidationError{
			Path:       "${" + name + "}",
			Message:    fmt.Sprintf("environment variable %q may not be referenced in config; only the INSTANCEZ_ENV_ namespace is allowed", name),
			Suggestion: "Rename it to INSTANCEZ_ENV_" + strings.TrimPrefix(name, "INSTANCEZ_"),
		})
	}
	return errs
}

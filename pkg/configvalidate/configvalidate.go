// Package configvalidate is the public surface for validating an ultrabase.yaml
// document without a database or the runtime environment. It wraps ultrabase's
// canonical parser + validator and returns plain structs, so callers outside the
// ultrabase module never import internal types.
package configvalidate

import "github.com/saedx1/instancez/internal/config"

// Problem is a single validation finding.
type Problem struct {
	Path       string `json:"path"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ValidateYAML parses config bytes with missing-env-tolerant interpolation, then
// runs the canonical ultrabase validator. Returns nil when the config is valid.
func ValidateYAML(data []byte) []Problem {
	cfg, err := config.ParseBytesLenient(data, "ultrabase.yaml")
	if err != nil {
		return []Problem{{Path: "", Message: err.Error()}}
	}
	var probs []Problem
	for _, ve := range config.Validate(cfg) {
		// Note: ultrabase's ValidationError.Line is not currently surfaced in Problem.
		probs = append(probs, Problem{Path: ve.Path, Message: ve.Message, Suggestion: ve.Suggestion})
	}
	return probs
}

// ScanEnvRefs returns the unique names of all ${VAR} references in data,
// extracted with ultrabase's canonical interpolation pattern.
func ScanEnvRefs(data []byte) []string {
	return config.EnvRefs(data)
}

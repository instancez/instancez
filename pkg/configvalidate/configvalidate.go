// Package configvalidate is the public surface for validating an instancez.yaml
// document without a database or the runtime environment. It wraps instancez's
// canonical parser + validator and returns plain structs, so callers outside the
// instancez module never import internal types.
package configvalidate

import (
	"github.com/instancez/instancez/internal/config"
	"gopkg.in/yaml.v3"
)

// Problem is a single validation finding.
type Problem struct {
	Path       string `json:"path"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ValidateYAML parses config bytes with missing-env-tolerant interpolation, then
// runs the canonical instancez validator. Returns nil when the config is valid.
func ValidateYAML(data []byte) []Problem {
	cfg, err := config.ParseBytesLenient(data, "instancez.yaml")
	if err != nil {
		return []Problem{{Path: "", Message: err.Error()}}
	}
	var probs []Problem
	for _, ve := range config.Validate(cfg) {
		// Note: instancez's ValidationError.Line is not currently surfaced in Problem.
		probs = append(probs, Problem{Path: ve.Path, Message: ve.Message, Suggestion: ve.Suggestion})
	}
	return probs
}

// ScanEnvRefs returns the unique names of all ${VAR} references in data,
// extracted with instancez's canonical interpolation pattern.
func ScanEnvRefs(data []byte) []string {
	return config.EnvRefs(data)
}

// MarshalYAML parses a JSON-encoded instancez config into the canonical
// domain.Config, validates it, and — when valid — emits the exact yaml.Marshal
// bytes that the instancez admin API's PUT /config and POST /config/preview
// write. This lets callers outside the instancez module (e.g. the platform's
// console config endpoints) produce byte-identical instancez.yaml documents and
// surface the same validation findings, without importing internal types.
//
// On a JSON decode failure it returns (nil, nil, err). When the config decodes
// but fails validation it returns (nil, problems, nil) — the same shape the
// dashboard renders. When valid it returns (yamlBytes, nil, nil).
func MarshalYAML(jsonBytes []byte) ([]byte, []Problem, error) {
	cfg, err := config.UnmarshalConfigJSON(jsonBytes)
	if err != nil {
		return nil, nil, err
	}
	// JSON-decoded configs skip the ParseBytes* loaders, so fill defaults here
	// before validating — otherwise defaultable fields (e.g. rpc.security) read
	// as "" and Validate rejects them.
	config.ApplyDefaults(cfg)
	if ves := config.Validate(cfg); len(ves) > 0 {
		probs := make([]Problem, 0, len(ves))
		for _, ve := range ves {
			probs = append(probs, Problem{Path: ve.Path, Message: ve.Message, Suggestion: ve.Suggestion})
		}
		return nil, probs, nil
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, nil, err
	}
	return out, nil, nil
}

// ValidateEnvNamespace rejects any ${VAR} reference in the raw config that is
// not in the INSTANCEZ_ENV_ namespace. Exposed so the platform enforces the
// exact rule the engine does, without importing internal types.
func ValidateEnvNamespace(raw []byte) []Problem {
	var probs []Problem
	for _, ve := range config.ValidateEnvNamespace(raw) {
		probs = append(probs, Problem{Path: ve.Path, Message: ve.Message, Suggestion: ve.Suggestion})
	}
	return probs
}

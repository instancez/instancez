// Package configvalidate is the public surface for validating an instancez.yaml
// document without a database or the runtime environment. It wraps instancez's
// canonical parser + validator and returns plain structs, so callers outside the
// instancez module never import internal types.
package configvalidate

import (
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/vet"
	"gopkg.in/yaml.v3"
)

// Problem is a single validation finding.
type Problem struct {
	Path       string `json:"path"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// toProblems maps domain.ValidationErrors to Problems; instancez's ValidationError.Line is not currently surfaced.
func toProblems(errs domain.ValidationErrors) []Problem {
	var probs []Problem
	for _, e := range errs {
		probs = append(probs, Problem{Path: e.Path, Message: e.Message, Suggestion: e.Suggestion})
	}
	return probs
}

// ValidateYAML parses config bytes with missing-env-tolerant interpolation, then
// runs the canonical instancez validator. Returns nil when the config is valid.
func ValidateYAML(data []byte) []Problem {
	cfg, err := config.ParseBytesLenient(data, "instancez.yaml")
	if err != nil {
		return []Problem{{Path: "", Message: err.Error()}}
	}
	return toProblems(config.Validate(cfg))
}

// WarningsYAML returns non-blocking findings, or nil on a parse failure (ValidateYAML reports those).
func WarningsYAML(data []byte) []Problem {
	cfg, err := config.ParseBytesLenient(data, "instancez.yaml")
	if err != nil {
		return nil
	}
	return toProblems(config.Warnings(cfg))
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

// VetFinding is one security finding from VetYAML.
type VetFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
	// Edit is the config change that resolves the finding; nil when the right change depends on the app.
	Edit *VetEdit `json:"edit,omitempty"`
}

// VetEdit is a config change: Path holds map keys and list indexes, and Remove deletes the value instead of setting Value.
type VetEdit = vet.Edit

// VetChecks counts the vet rules; Passed is those with no finding.
type VetChecks = vet.Checks

// VetResult is the findings plus the checks tally from VetYAML.
type VetResult struct {
	Findings []VetFinding `json:"findings"`
	Checks   VetChecks    `json:"checks"`
}

// VetYAML runs the `inz vet` security rules on config bytes; the error is a parse failure.
func VetYAML(data []byte) (*VetResult, error) {
	rep, err := vet.Run(data)
	if err != nil {
		return nil, err
	}
	out := &VetResult{Findings: make([]VetFinding, 0, len(rep.Findings)), Checks: rep.Checks}
	for _, f := range rep.Findings {
		out.Findings = append(out.Findings, VetFinding{f.Rule, f.Severity.String(), f.Path, f.Line, f.Title, f.Message, f.Fix, f.Edit})
	}
	return out, nil
}

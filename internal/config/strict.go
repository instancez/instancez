package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

// decodeYAMLStrict decodes YAML into cfg and records any key that does not map
// to a struct field on cfg.UnknownKeys (which Validate later surfaces). Unknown
// keys are NOT fatal: the struct is still fully populated, so structural
// validation runs and the caller sees every problem in one pass. Map-keyed
// sections (tables, storage, rpc, functions, auth.oauth, auth.email.templates)
// still accept arbitrary keys because KnownFields only constrains struct fields.
// A genuine syntax error (not an unknown-field TypeError) is returned as fatal.
func decodeYAMLStrict(data []byte, cfg *domain.Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err := dec.Decode(cfg)
	if err == nil {
		return nil
	}
	var te *yaml.TypeError
	if errors.As(err, &te) {
		cfg.UnknownKeys = append(cfg.UnknownKeys, unknownKeyProblems(te)...)
		return nil
	}
	return err
}

// yamlUnknownFieldRe matches yaml.v3's KnownFields error sub-message, e.g.
// `line 48: field google not found in type domain.Auth`.
var yamlUnknownFieldRe = regexp.MustCompile(`^line (\d+): field (\S+) not found in type (\S+)$`)

// yamlTypeToSection maps a Go struct type name (as yaml.v3 reports it) to the
// config path the user wrote it under, so the message reads in their terms.
// domain.Config is the top level and intentionally absent (no "under" suffix).
var yamlTypeToSection = map[string]string{
	"domain.Auth":            "auth",
	"domain.AuthEmail":       "auth.email",
	"domain.EmailTemplate":   "auth.email.templates.<name>",
	"domain.OAuthProvider":   "auth.oauth.<name>",
	"domain.Server":          "server",
	"domain.Project":         "project",
	"domain.ProjectCloud":    "project.cloud",
	"domain.Providers":       "providers",
	"domain.EmailProvider":   "providers.email",
	"domain.StorageProvider": "providers.storage",
	"domain.DatabaseConfig":  "database",
	"domain.Table":           "tables.<name>",
	"domain.Field":           "tables.<name>.fields[]",
	"domain.Index":           "tables.<name>.indexes[]",
	"domain.RLSPolicy":       "tables.<name>.rls[]",
	"domain.Function":        "rpc.<name>",
	"domain.CodeFunction":    "functions.<name>",
	"domain.Bucket":          "storage.<name>",
}

// unknownKeyProblems converts a yaml.v3 TypeError into one ValidationError per
// unknown key. A sub-message that doesn't match the known shape is passed
// through verbatim so nothing is silently lost.
func unknownKeyProblems(te *yaml.TypeError) domain.ValidationErrors {
	out := make(domain.ValidationErrors, 0, len(te.Errors))
	for _, sub := range te.Errors {
		m := yamlUnknownFieldRe.FindStringSubmatch(sub)
		if m == nil {
			out = append(out, &domain.ValidationError{Message: sub})
			continue
		}
		line, _ := strconv.Atoi(m[1])
		field, typ := m[2], m[3]
		path := "(top level)"
		if section, ok := yamlTypeToSection[typ]; ok {
			path = section
		}
		out = append(out, &domain.ValidationError{
			Path:    path,
			Line:    line,
			Message: fmt.Sprintf("unknown key %q", field),
		})
	}
	return out
}

// UnmarshalConfigJSON decodes a JSON config body. Unknown fields are recorded on
// cfg.UnknownKeys (surfaced by Validate), not treated as fatal. This mirrors the
// YAML path so the dashboard edit surface aggregates unknown keys with the rest.
// It does not apply defaults; callers that need them call ApplyDefaults after.
func UnmarshalConfigJSON(data []byte) (*domain.Config, error) {
	// First a lenient pass to populate the struct regardless of unknown fields.
	var cfg domain.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	// Then a strict pass purely to detect unknown fields. encoding/json reports
	// the first unknown field per decode; loop, stripping each as we find it, to
	// report them all.
	for _, key := range jsonUnknownFields(data) {
		cfg.UnknownKeys = append(cfg.UnknownKeys, &domain.ValidationError{Message: fmt.Sprintf("unknown key %q", key)})
	}
	return &cfg, nil
}

var jsonUnknownFieldRe = regexp.MustCompile(`json: unknown field "(.+)"`)

// jsonUnknownFields returns every unknown field name in a JSON config body.
// encoding/json surfaces one unknown field per Decode, so we re-decode while
// trimming the seen fields out of the candidate set until none remain.
func jsonUnknownFields(data []byte) []string {
	var found []string
	seen := map[string]bool{}
	for {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		var sink domain.Config
		err := dec.Decode(&sink)
		if err == nil {
			return found
		}
		m := jsonUnknownFieldRe.FindStringSubmatch(err.Error())
		if m == nil || seen[m[1]] {
			// Not an unknown-field error, or we've already recorded it and can't
			// make progress, so stop to avoid looping.
			return found
		}
		seen[m[1]] = true
		found = append(found, m[1])
		data = stripJSONField(data, m[1])
	}
}

// stripJSONField removes the top-level "name": <value> member so the next strict
// decode surfaces the following unknown field instead of the same one.
func stripJSONField(data []byte, name string) []byte {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return data
	}
	delete(raw, name)
	out, err := json.Marshal(raw)
	if err != nil {
		return data
	}
	return out
}

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

// strictUnmarshalYAML decodes YAML into cfg and rejects any key that does not
// map to a struct field. Map-keyed sections (tables, storage, rpc, functions,
// auth.oauth, auth.email.templates) still accept arbitrary keys because
// KnownFields only constrains struct fields.
func strictUnmarshalYAML(data []byte, cfg *domain.Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return translateUnknownKeyErr(err)
	}
	return nil
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

// translateUnknownKeyErr converts a yaml.v3 TypeError into a readable message
// listing every unknown key. Non-TypeErrors pass through unchanged.
func translateUnknownKeyErr(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return err
	}
	msgs := make([]string, 0, len(te.Errors))
	for _, sub := range te.Errors {
		msgs = append(msgs, friendlyYAMLFieldErr(sub))
	}
	return errors.New(strings.Join(msgs, "; "))
}

func friendlyYAMLFieldErr(sub string) string {
	m := yamlUnknownFieldRe.FindStringSubmatch(sub)
	if m == nil {
		return sub // some other type error; show it verbatim
	}
	line, field, typ := m[1], m[2], m[3]
	if section, ok := yamlTypeToSection[typ]; ok {
		return fmt.Sprintf("line %s: unknown key %q under %s", line, field, section)
	}
	return fmt.Sprintf("line %s: unknown key %q", line, field)
}

// UnmarshalConfigJSON decodes a JSON config body, rejecting unknown fields. It
// does not apply defaults; callers that need them call ApplyDefaults afterward,
// matching the previous json.Unmarshal behavior.
func UnmarshalConfigJSON(data []byte) (*domain.Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg domain.Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, translateJSONUnknownKeyErr(err)
	}
	return &cfg, nil
}

var jsonUnknownFieldRe = regexp.MustCompile(`^json: unknown field "(.+)"$`)

func translateJSONUnknownKeyErr(err error) error {
	if m := jsonUnknownFieldRe.FindStringSubmatch(err.Error()); m != nil {
		return fmt.Errorf("unknown key %q", m[1])
	}
	return err
}

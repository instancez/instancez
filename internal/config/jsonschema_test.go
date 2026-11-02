package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var updateSchema = flag.Bool("update", false, "rewrite the JSON Schema golden file")

const schemaGolden = "../../docs/site/public/schema/instancez.json"

func TestJSONSchemaGolden(t *testing.T) {
	got, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if *updateSchema {
		if err := os.WriteFile(schemaGolden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(schemaGolden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("schema is out of date; run: go test ./internal/config -run TestJSONSchemaGolden -update")
	}
}

func TestJSONSchemaDeterministic(t *testing.T) {
	a, _ := JSONSchema()
	for range 5 {
		if b, _ := JSONSchema(); !bytes.Equal(a, b) {
			t.Fatal("schema output differs between runs")
		}
	}
}

func dig(t *testing.T, v any, path ...string) any {
	t.Helper()
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("at %q: not an object: %v", p, v)
		}
		if v, ok = m[p]; !ok {
			t.Fatalf("missing %q", p)
		}
	}
	return v
}

func TestJSONSchemaShape(t *testing.T) {
	raw, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" || s["$id"] != SchemaURL {
		t.Errorf("header: %v %v", s["$schema"], s["$id"])
	}
	table := dig(t, s, "properties", "tables", "additionalProperties", "properties")
	if dig(t, table, "fields", "type") != "array" {
		t.Error("fields must be an array")
	}
	ops := dig(t, table, "rls", "items", "properties", "operations", "items", "enum")
	if !reflect.DeepEqual(ops, []any{"delete", "insert", "select", "update"}) {
		t.Errorf("operations enum = %v", ops)
	}
	field := dig(t, table, "fields", "items", "properties")
	if !reflect.DeepEqual(dig(t, field, "foreign_key", "properties", "on_delete", "enum"), []any{"cascade", "restrict", "set_null"}) {
		t.Error("on_delete enum")
	}
	if m := dig(t, table, "indexes", "items", "properties", "method", "enum").([]any); len(m) != 6 {
		t.Errorf("method enum = %v", m)
	}
	port := dig(t, s, "properties", "server", "properties", "port", "anyOf").([]any)
	if len(port) != 2 || dig(t, port[0], "type") != "integer" || dig(t, port[1], "pattern") != envStringPattern {
		t.Errorf("port anyOf = %v", port)
	}
	if dig(t, s, "additionalProperties") != false || dig(t, s, "properties", "server", "additionalProperties") != false {
		t.Error("structs must reject unknown keys")
	}
	if dig(t, field, "ref", "deprecated") != true || dig(t, field, "on_delete", "deprecated") != true {
		t.Error("ref and on_delete must be deprecated")
	}
	if _, ok := dig(t, s, "properties").(map[string]any)["unknownkeys"]; ok {
		t.Error("yaml:\"-\" fields must be absent")
	}
	if _, ok := field.(map[string]any)["returncategory"]; ok {
		t.Error("ReturnCategory leaked")
	}
	if strings.Contains(string(raw), "UnknownKeys") {
		t.Error("UnknownKeys leaked")
	}
}

func TestJSONSchemaAcceptsShippedConfigs(t *testing.T) {
	raw, _ := JSONSchema()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for name, src := range shippedConfigs(t) {
		var doc any
		if err := yaml.Unmarshal(envVarPattern.ReplaceAll(src, []byte("x")), &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p := schemaMismatch(schema, doc, ""); p != "" {
			t.Errorf("%s: key %s is not in the schema", name, p)
		}
	}
}

// schemaMismatch returns the first key path in doc that the schema's properties do not allow.
func schemaMismatch(schema map[string]any, doc any, path string) string {
	switch d := doc.(type) {
	case map[string]any:
		props, hasProps := schema["properties"].(map[string]any)
		extra, _ := schema["additionalProperties"].(map[string]any)
		for k, v := range d {
			sub, ok := props[k].(map[string]any)
			if !ok {
				if hasProps || extra == nil {
					return path + "." + k
				}
				sub = extra
			}
			if p := schemaMismatch(sub, v, path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for _, el := range d {
			if p := schemaMismatch(items, el, path+"[]"); p != "" {
				return p
			}
		}
	}
	return ""
}

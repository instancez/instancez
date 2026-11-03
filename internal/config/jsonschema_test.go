package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
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
	if !reflect.DeepEqual(dig(t, table, "fields", "type"), []any{"array", "null"}) {
		t.Error("fields must be an array")
	}
	ops := dig(t, table, "rls", "items", "properties", "operations", "items", "enum")
	if !reflect.DeepEqual(ops, []any{"delete", "insert", "select", "update"}) {
		t.Errorf("operations enum = %v", ops)
	}
	field := dig(t, table, "fields", "items", "properties")
	if !reflect.DeepEqual(dig(t, field, "foreign_key", "properties", "on_delete", "enum"), []any{"cascade", "restrict", "set_null", nil}) {
		t.Error("on_delete enum")
	}
	if m := dig(t, table, "indexes", "items", "properties", "method", "enum").([]any); len(m) != 7 {
		t.Errorf("method enum = %v", m)
	}
	port := dig(t, s, "properties", "server", "properties", "port", "anyOf").([]any)
	if len(port) != 3 || dig(t, port[0], "type") != "integer" || dig(t, port[1], "pattern") != envStringPattern {
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

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(SchemaURL, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(SchemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

var quotedEnvRef = regexp.MustCompile(`\$\{[^}]*\}`)

// yamlToJSONValue decodes yaml (quoting ${VAR} refs when flow style needs it) into a validator-ready value.
func yamlToJSONValue(t *testing.T, src []byte) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(src, &v); err != nil {
		if err = yaml.Unmarshal(quotedEnvRef.ReplaceAll(src, []byte(`"$0"`)), &v); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestJSONSchemaAcceptsShippedConfigs(t *testing.T) {
	sch := compileSchema(t)
	for name, src := range shippedConfigs(t) {
		t.Run(name, func(t *testing.T) {
			if err := sch.Validate(yamlToJSONValue(t, src)); err != nil {
				t.Fatalf("as written: %v", err)
			}
			cfg, err := ParseBytesLenient(src, name)
			if err != nil {
				t.Fatal(err)
			}
			out, err := yaml.Marshal(cfg) // what the dashboard saves
			if err != nil {
				t.Fatal(err)
			}
			if err := sch.Validate(yamlToJSONValue(t, out)); err != nil {
				t.Fatalf("dashboard-style marshal: %v", err)
			}
		})
	}
}

func TestJSONSchemaRejectsMistakes(t *testing.T) {
	sch := compileSchema(t)
	for name, src := range map[string]string{
		"unknown key": "version: 1\nbogus: 1\n",
		"bad enum":    "version: 1\ntables:\n  t:\n    fields:\n      - {name: a, type: text, foreign_key: {references: x.id, on_delete: explode}}\n",
		"string port": "version: 1\nserver:\n  port: abc\n",
		"bad rls op":  "version: 1\ntables:\n  t:\n    rls:\n      - {operations: [selec]}\n",
	} {
		if err := sch.Validate(yamlToJSONValue(t, []byte(src))); err == nil {
			t.Errorf("%s: schema accepted it", name)
		}
	}
	if err := sch.Validate(yamlToJSONValue(t, []byte("version: 1\nserver:\n  port: ${PORT}\nproviders:\n  email: null\ntables:\n  t:\n    rls: null\n    fields: null\n"))); err != nil {
		t.Errorf("null optionals and env refs must pass: %v", err)
	}
}

func TestJSONSchemaAcceptsShorthandsAndInclude(t *testing.T) {
	sch := compileSchema(t)
	src := "version: 1\ninclude: [tables/*.yaml, rpc.yaml]\ntables:\n  t:\n    timestamps: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n      - {name: user_id, type: uuid}\n    rls:\n      - owner: user_id\n      - {operations: [insert, update], owner: user_id}\nstorage:\n  b:\n    rls:\n      - {owner: uploaded_by}\n"
	if err := sch.Validate(yamlToJSONValue(t, []byte(src))); err != nil {
		t.Fatalf("shorthands and include must pass: %v", err)
	}
	for name, bad := range map[string]string{
		"include scalar":  "version: 1\ninclude: frag.yaml\n",
		"owner list":      "version: 1\ntables:\n  t:\n    rls:\n      - {owner: [a]}\n",
		"timestamps text": "version: 1\ntables:\n  t:\n    timestamps: maybe\n",
		"unknown rls key": "version: 1\ntables:\n  t:\n    rls:\n      - {ownr: user_id}\n",
	} {
		if err := sch.Validate(yamlToJSONValue(t, []byte(bad))); err == nil {
			t.Errorf("%s: schema accepted it", name)
		}
	}
}

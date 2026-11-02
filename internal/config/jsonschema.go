package config

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// SchemaURL is where the generated JSON Schema is published.
const SchemaURL = "https://docs.instancez.ai/schema/instancez.json"

// SchemaHeader is the yaml-language-server comment that points editors at the schema.
const SchemaHeader = "# yaml-language-server: $schema=" + SchemaURL

const envStringPattern = `^\$\{[A-Za-z_][A-Za-z0-9_]*(:-.*)?\}$`

func enumKeys(m map[string]bool) []string {
	keys := slices.Sorted(maps.Keys(m))
	return slices.DeleteFunc(keys, func(s string) bool { return s == "" })
}

// schemaEnums are string enums keyed by "StructName.GoField"; slices apply them to their items.
func schemaEnums() map[string][]string {
	return map[string][]string{
		"ForeignKey.OnDelete":  enumKeys(validOnDelete),
		"RLSPolicy.Operations": enumKeys(validRLSOps),
		"RLSPolicy.Type":       enumKeys(validRLSTypes),
		"Index.Method":         slices.Clone(domain.IndexMethods),
		"EmailProvider.Type":   enumKeys(validEmailProviders),
		"StorageProvider.Type": enumKeys(validStorageProviders),
		"Function.Language":    enumKeys(validRPCLanguages),
		"Function.Volatility":  enumKeys(validRPCVolatility),
		"Function.Security":    enumKeys(validRPCSecurity),
	}
}

// schemaDeprecated lists fields that still parse but do nothing.
var schemaDeprecated = map[string]bool{"Field.Ref": true, "Field.OnDelete": true}

// JSONSchema renders the instancez.yaml JSON Schema by reflecting over domain.Config.
func JSONSchema() ([]byte, error) {
	g := &schemaGen{enums: schemaEnums()}
	root := g.typeSchema(reflect.TypeOf(domain.Config{}), "")
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$id"] = SchemaURL
	root["title"] = "instancez.yaml"
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

type schemaGen struct{ enums map[string][]string }

func (g *schemaGen) typeSchema(t reflect.Type, enumKey string) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		return g.structSchema(t)
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": g.typeSchema(t.Elem(), enumKey)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.typeSchema(t.Elem(), "")}
	case reflect.Bool:
		return envOr("boolean")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return envOr("integer")
	case reflect.Float32, reflect.Float64:
		return envOr("number")
	case reflect.String:
		s := map[string]any{"type": "string"}
		if e, ok := g.enums[enumKey]; ok {
			s["enum"] = e
		}
		return s
	}
	return map[string]any{}
}

func envOr(kind string) map[string]any {
	return map[string]any{"anyOf": []any{
		map[string]any{"type": kind},
		map[string]any{"type": "string", "pattern": envStringPattern},
	}}
}

func (g *schemaGen) structSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	g.addFields(t, props)
	return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
}

func (g *schemaGen) addFields(t reflect.Type, props map[string]any) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("yaml")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if strings.Contains(opts, "inline") {
			g.addFields(f.Type, props)
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		key := t.Name() + "." + f.Name
		s := g.typeSchema(f.Type, key)
		if schemaDeprecated[key] {
			s["deprecated"] = true
		}
		props[name] = s
	}
}

package typegen

import (
	"slices"
	"sort"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

const jsonType = `export type Json =
  | string
  | number
  | boolean
  | null
  | { [key: string]: Json | undefined }
  | Json[]
`

const maxWidth = 80

type writer struct{ strings.Builder }

func indent(depth int) string { return strings.Repeat("  ", depth) }

func (w *writer) line(depth int, s string) { w.WriteString(indent(depth) + s + "\n") }

func field(depth int, name string, optional bool, members []string) string {
	k := key(name)
	if optional {
		k += "?"
	}
	one := indent(depth) + k + ": " + strings.Join(members, " | ")
	if len(members) < 2 || displayWidth(one) <= maxWidth {
		return one + "\n"
	}
	var b strings.Builder
	b.WriteString(indent(depth) + k + ":\n")
	for _, m := range members {
		b.WriteString(indent(depth+1) + "| " + m + "\n")
	}
	return b.String()
}

func (w *writer) neverBlock(depth int, name string) {
	w.line(depth, name+": {")
	w.line(depth+1, "[_ in never]: never")
	w.line(depth, "}")
}

func columnMembers(f domain.Field, typ string, nullable bool) []string {
	members := []string{tsType(typ)}
	if len(f.Enum) > 0 && !strings.HasSuffix(strings.TrimSpace(typ), "[]") {
		members = members[:0]
		for _, v := range f.Enum {
			members = append(members, quoteString(v))
		}
	}
	if nullable {
		members = append(members, "null")
	}
	return members
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (w *writer) table(name string, t domain.Table, resolve domain.FieldResolver) {
	fields := slices.Clone(t.Fields)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	w.line(3, key(name)+": {")
	for _, section := range []string{"Row", "Insert", "Update"} {
		if len(fields) == 0 {
			w.line(4, section+": {}")
			continue
		}
		w.line(4, section+": {")
		for _, f := range fields {
			typ := domain.EffectiveType(f, resolve)
			serial := isSerial(typ)
			nullable := !(f.Required || f.PrimaryKey || serial)
			switch section {
			case "Row":
				w.WriteString(field(5, f.Name, false, columnMembers(f, typ, nullable)))
			case "Insert":
				optional := f.Default != nil || serial || nullable
				w.WriteString(field(5, f.Name, optional, columnMembers(f, typ, nullable)))
			default:
				if f.Immutable {
					w.WriteString(field(5, f.Name, true, []string{"never"}))
				} else {
					w.WriteString(field(5, f.Name, true, columnMembers(f, typ, nullable)))
				}
			}
		}
		w.line(4, "}")
	}
	w.line(4, "Relationships: []")
	w.line(3, "}")
}

// Generate returns the TypeScript Database types for cfg.
func Generate(cfg *domain.Config) []byte {
	if cfg == nil {
		cfg = &domain.Config{}
	}
	resolve := domain.ResolverOver(cfg.Tables)
	bySchema := map[string][]string{"public": nil}
	for name, t := range cfg.Tables {
		s := t.EffectiveSchema()
		bySchema[s] = append(bySchema[s], name)
	}

	w := &writer{}
	w.WriteString(jsonType + "\n")
	w.line(0, "export type Database = {")
	for _, schema := range sortedKeys(bySchema) {
		names := bySchema[schema]
		sort.Strings(names)
		w.line(1, key(schema)+": {")
		if len(names) == 0 {
			w.neverBlock(2, "Tables")
		} else {
			w.line(2, "Tables: {")
			for _, n := range names {
				w.table(n, cfg.Tables[n], resolve)
			}
			w.line(2, "}")
		}
		w.neverBlock(2, "Views")
		w.neverBlock(2, "Functions")
		w.neverBlock(2, "Enums")
		w.neverBlock(2, "CompositeTypes")
		w.line(1, "}")
	}
	w.line(0, "}")
	w.WriteString(helperTypes)
	return []byte(w.String())
}

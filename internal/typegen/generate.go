package typegen

import (
	"maps"
	"slices"
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
	var members []string
	if len(f.Enum) > 0 && !strings.HasSuffix(strings.TrimSpace(typ), "[]") {
		for _, v := range f.Enum {
			members = append(members, quoteString(v))
		}
	} else {
		members = []string{tsType(typ)}
	}
	if nullable {
		members = append(members, "null")
	}
	return members
}

func (w *writer) table(name string, t domain.Table, resolve domain.FieldResolver) {
	fields := slices.Clone(t.Fields)
	slices.SortFunc(fields, func(a, b domain.Field) int { return strings.Compare(a.Name, b.Name) })
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
			members := columnMembers(f, typ, nullable)
			optional := true
			switch section {
			case "Row":
				optional = false
			case "Insert":
				optional = f.Default != nil || serial || nullable
			default:
				if f.Immutable {
					members = []string{"never"}
				}
			}
			w.WriteString(field(5, f.Name, optional, members))
		}
		w.line(4, "}")
	}
	w.relationships(4, name, t, fields)
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
	for _, schema := range slices.Sorted(maps.Keys(bySchema)) {
		names := bySchema[schema]
		slices.Sort(names)
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
		w.functions(2, rpcFor(schema, cfg.RPC), cfg.Tables)
		w.neverBlock(2, "Enums")
		w.neverBlock(2, "CompositeTypes")
		w.line(1, "}")
	}
	w.line(0, "}")
	w.WriteString(helperTypes)
	return []byte(w.String())
}

func rpcFor(schema string, rpc map[string]domain.Function) map[string]domain.Function {
	if schema != "public" {
		return nil
	}
	return rpc
}

package typegen

import (
	"maps"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

func (w *writer) functions(depth int, rpc map[string]domain.Function, tables map[string]domain.Table) {
	if len(rpc) == 0 {
		w.neverBlock(depth, "Functions")
		return
	}
	w.line(depth, "Functions: {")
	for _, name := range slices.Sorted(maps.Keys(rpc)) {
		fn := rpc[name]
		w.line(depth+1, key(name)+": {")
		w.args(depth+2, fn.Args)
		w.returns(depth+2, name, fn, tables)
		w.line(depth+1, "}")
	}
	w.line(depth, "}")
}

func (w *writer) args(depth int, args []domain.FuncArg) {
	if len(args) == 0 {
		w.line(depth, "Args: Record<PropertyKey, never>")
		return
	}
	w.line(depth, "Args: {")
	sorted := slices.Clone(args)
	slices.SortFunc(sorted, func(a, b domain.FuncArg) int { return strings.Compare(a.Name, b.Name) })
	for _, a := range sorted {
		optional := !a.Required && a.Default != nil
		w.WriteString(field(depth+1, a.Name, optional, []string{tsType(a.Type)}))
	}
	w.line(depth, "}")
}

func (w *writer) returns(depth int, name string, fn domain.Function, tables map[string]domain.Table) {
	t := strings.TrimSpace(fn.Returns.Type)
	switch {
	case fn.ReturnCategory == "void":
		w.line(depth, "Returns: undefined")
	case fn.ReturnCategory == "setof" && !strings.HasPrefix(strings.ToLower(t), "setof"):
		w.objectArray(depth, tableColumns(t))
	case fn.ReturnCategory == "setof":
		target := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(t[len("setof"):])), "public.")
		ts := tsType(target)
		if tbl, ok := tables[target]; ok {
			w.line(depth, "Returns: Database["+quoteString(tbl.EffectiveSchema())+`]["Tables"][`+quoteString(target)+`]["Row"][]`)
			// Table names are unique across schemas, so `to` is the bare name.
			w.line(depth, "SetofOptions: {")
			w.line(depth+1, `from: "*"`)
			w.line(depth+1, "to: "+quoteString(target))
			w.line(depth+1, "isOneToOne: false")
			w.line(depth+1, "isSetofReturn: true")
			w.line(depth, "}")
		} else if ts != "unknown" {
			w.objectArray(depth, [][2]string{{name, ts}})
		} else {
			w.line(depth, "Returns: Record<string, unknown>[]")
		}
	default:
		w.line(depth, "Returns: "+tsType(t))
	}
}

// objectArray prints an inline array of objects whose members are all nullable.
func (w *writer) objectArray(depth int, cols [][2]string) {
	if len(cols) == 0 {
		w.line(depth, "Returns: Record<string, never>[]")
		return
	}
	w.line(depth, "Returns: {")
	for _, c := range cols {
		w.WriteString(field(depth+1, c[0], false, []string{c[1], "null"}))
	}
	w.line(depth, "}[]")
}

// tableColumns parses `table(a int, b numeric(10,2))` into name and TS type pairs.
func tableColumns(t string) [][2]string {
	inner := t[strings.Index(t, "(")+1 : strings.LastIndex(t, ")")]
	var cols [][2]string
	for _, part := range splitTopLevel(inner) {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		cols = append(cols, [2]string{f[0], tsType(strings.Join(f[1:], " "))})
	}
	return cols
}

func splitTopLevel(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

type relationship struct {
	field    domain.Field
	refTable string
	refCol   string
}

func (w *writer) relationships(depth int, name string, t domain.Table, fields []domain.Field, cfg *domain.Config) {
	var rels []relationship
	for _, f := range fields {
		if f.ForeignKey == nil {
			continue
		}
		schema, tbl, col, err := domain.ParseFKReference(f.ForeignKey.References)
		if err != nil || schema != t.EffectiveSchema() {
			continue
		}
		if target, ok := cfg.Tables[tbl]; ok && target.EffectiveSchema() == schema {
			rels = append(rels, relationship{f, tbl, col})
		}
	}
	if len(rels) == 0 {
		w.line(depth, "Relationships: []")
		return
	}
	w.line(depth, "Relationships: [")
	for _, r := range rels {
		w.line(depth+1, "{")
		w.line(depth+2, "foreignKeyName: "+quoteString(domain.FKConstraintName(name, r.field.Name)))
		w.stringList(depth+2, "columns", r.field.Name)
		w.line(depth+2, "isOneToOne: false")
		w.line(depth+2, "referencedRelation: "+quoteString(r.refTable))
		w.stringList(depth+2, "referencedColumns", r.refCol)
		w.line(depth+1, "},")
	}
	w.line(depth, "]")
}

// stringList prints a one-element string array, broken the way prettier breaks it past 80 columns.
func (w *writer) stringList(depth int, k, v string) {
	one := indent(depth) + k + ": [" + quoteString(v) + "]"
	if displayWidth(one) <= maxWidth {
		w.WriteString(one + "\n")
		return
	}
	w.line(depth, k+": [")
	w.line(depth+1, quoteString(v)+",")
	w.line(depth, "]")
}

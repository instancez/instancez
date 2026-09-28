package postgrest

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// sortedMapKeys returns the sorted keys of a map[string]any.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseSelectParam splits a comma-separated select string at top level
// (respecting parentheses) and returns the individual items.
func ParseSelectParam(sel string) []string {
	var result []string
	depth := 0
	start := 0
	for i, ch := range sel {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, strings.TrimSpace(sel[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(sel) {
		result = append(result, strings.TrimSpace(sel[start:]))
	}
	return result
}

// ParseOrderValue parses a comma-separated PostgREST order list into
// OrderClauses, validating columns against the table.
func ParseOrderValue(val string, table domain.Table) ([]OrderClause, error) {
	return ParseOrderValueWith(val, func(col string) error { return ValidateColumn(table, col) })
}

// NullIfNoMatch makes a to-one embed JSON null when its LEFT JOIN found no row.
func NullIfNoMatch(alias, refCol, expr string) string {
	return fmt.Sprintf("CASE WHEN %s.%s IS NULL THEN NULL ELSE %s END", alias, refCol, expr)
}

// HasBelongsToJoin reports whether any embed will produce a JOIN on the
// outer FROM clause.
func HasBelongsToJoin(embeds []Embed) bool {
	for _, e := range embeds {
		if !e.IsReverse {
			return true
		}
	}
	return false
}

// QualifyOrderColumns prefixes each non-alias ORDER clause column with
// "<table>." so it's unambiguous against joined embed columns.
func QualifyOrderColumns(clauses []OrderClause, tableName string) []OrderClause {
	if len(clauses) == 0 {
		return clauses
	}
	out := make([]OrderClause, len(clauses))
	for i, oc := range clauses {
		out[i] = oc
		if !oc.IsAlias && !strings.Contains(oc.Column, ".") {
			out[i].Column = tableName + "." + oc.Column
		}
	}
	return out
}

// AliasWhereColumns returns a clone of n with every leaf column prefixed
// with "<alias>.".
func AliasWhereColumns(n *WhereNode, alias string) *WhereNode {
	if n == nil {
		return nil
	}
	if n.Leaf != nil {
		f := *n.Leaf
		f.Column = alias + "." + f.Column
		return &WhereNode{Leaf: &f, Not: n.Not}
	}
	clone := &WhereNode{Op: n.Op, Not: n.Not}
	for _, c := range n.Children {
		clone.Children = append(clone.Children, AliasWhereColumns(c, alias))
	}
	return clone
}

// RenderOrderBy emits a comma-separated ORDER BY list from OrderClauses.
func RenderOrderBy(clauses []OrderClause) string {
	parts := make([]string, 0, len(clauses))
	for _, o := range clauses {
		dir := "ASC"
		if o.Desc {
			dir = "DESC"
		}
		col := o.Column
		if o.IsAlias {
			col = `"` + strings.ReplaceAll(o.Column, `"`, `""`) + `"`
		}
		c := fmt.Sprintf("%s %s", col, dir)
		switch o.Nulls {
		case "first":
			c += " NULLS FIRST"
		case "last":
			c += " NULLS LAST"
		}
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

// ParseEmbedParam parses "author(id,name)" into name, alias, cols, and nested
// embed raw strings.
func ParseEmbedParam(s string) (name, alias string, cols []string, nested []string, spread bool) {
	idx := strings.Index(s, "(")
	if idx == -1 {
		name = s
		if strings.HasPrefix(name, "...") {
			spread = true
			name = name[3:]
		}
		alias, name = SplitEmbedAlias(name)
		return
	}
	name = s[:idx]
	if strings.HasPrefix(name, "...") {
		spread = true
		name = name[3:]
	}
	alias, name = SplitEmbedAlias(name)
	inner := s[idx+1 : len(s)-1] // strip parens
	if inner == "*" || inner == "" {
		return
	}
	items, err := SplitTopLevel(inner, ',')
	if err != nil {
		cols = []string{inner}
		return
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || item == "*" {
			continue
		}
		if strings.Contains(item, "(") {
			nested = append(nested, item)
		} else {
			cols = append(cols, item)
		}
	}
	return
}

// validateEmbedBalance rejects unbalanced parentheses before ParseEmbedParam slices on them.
func validateEmbedBalance(raw string) error {
	if strings.Contains(raw, "(") && !strings.HasSuffix(raw, ")") {
		return fmt.Errorf("unbalanced parentheses in embed %q", raw)
	}
	if _, err := SplitTopLevel(raw, ','); err != nil {
		return fmt.Errorf("embed %q: %w", raw, err)
	}
	return nil
}

// validateEmbedSpec rejects embed columns and aliases unsafe to interpolate.
func validateEmbedSpec(raw, alias string, cols []string, ref domain.Table) error {
	if alias != "" && !identRe.MatchString(alias) {
		return fmt.Errorf("invalid embed alias %q", alias)
	}
	for _, c := range cols {
		if _, ok := ref.GetField(c); !ok {
			return fmt.Errorf("unknown column %q in embed", c)
		}
	}
	return nil
}

// ResolveEmbeds resolves embed names to FK relationships using the table config.
func ResolveEmbeds(tableName string, table domain.Table, embedNames []string, allTables map[string]domain.Table) ([]Embed, error) {
	var embeds []Embed

	for _, raw := range embedNames {
		if err := validateEmbedBalance(raw); err != nil {
			return nil, err
		}
		name, alias, cols, nested, spread := ParseEmbedParam(raw)
		name, inner, fkHint := ParseEmbedHint(name)
		if spread && alias != "" {
			return nil, fmt.Errorf("alias not allowed on spread embed %q", alias+":"+name)
		}

		cands := slices.Concat(
			belongsToRels(tableName, table, name, fkHint, allTables),
			hasManyRels(tableName, name, fkHint, allTables),
			junctionRels(tableName, name, fkHint, allTables))
		if len(cands) > 1 {
			return nil, ambiguousEmbed(tableName, name, cands)
		}
		if len(cands) == 0 {
			return nil, fmt.Errorf("could not find a relationship between %q and %q in the schema", tableName, name)
		}
		emb := cands[0].emb
		if spread && emb.IsReverse {
			return nil, fmt.Errorf("spread (...) not allowed on has-many embed %q", name)
		}
		emb.Name, emb.Alias, emb.Columns, emb.Inner, emb.Spread = name, alias, cols, inner, spread
		refTbl, ok := allTables[emb.RefTable]
		if err := validateEmbedSpec(raw, alias, cols, refTbl); err != nil {
			return nil, err
		}
		if len(nested) > 0 {
			if !ok {
				return nil, fmt.Errorf("embed %q references unknown table %q", name, emb.RefTable)
			}
			children, err := ResolveEmbeds(emb.RefTable, refTbl, nested, allTables)
			if err != nil {
				return nil, fmt.Errorf("nested embed in %q: %w", name, err)
			}
			emb.Children = children
		}
		embeds = append(embeds, emb)
	}

	return embeds, nil
}

// relCandidate is one relationship an embed name matched, with its PGRST201 description.
type relCandidate struct {
	emb          Embed
	cardinality  string
	relationship string
	constraint   string
}

func fkConstraintName(table, col string) string {
	// ponytail: PG truncates names past 63 bytes differently; long names won't match as hints.
	return table + "_" + col + "_fkey"
}

// matchesFKHint accepts the constraint name or either column of the FK, as PostgREST does.
func matchesFKHint(hint, table, col, refCol string) bool {
	return hint == col || hint == refCol || strings.TrimSuffix(col, "_id") == hint || hint == fkConstraintName(table, col)
}

// fkTarget splits an FK reference; a schema-qualified one must name a configured table in that schema.
func fkTarget(ref string, allTables map[string]domain.Table) (table, col string, ok bool) {
	schema, table, col, err := domain.ParseFKReference(ref)
	if err != nil {
		return "", "", false
	}
	if strings.Count(ref, ".") == 2 {
		t, found := allTables[table]
		if !found || t.EffectiveSchema() != schema {
			return "", "", false
		}
	}
	return table, col, true
}

// isOneToOne mirrors PostgREST: the FK columns are exactly a primary or unique key.
func isOneToOne(t domain.Table, f domain.Field) bool {
	return f.Unique || (f.PrimaryKey && len(PrimaryKeyColumns(t)) == 1)
}

func belongsToRels(tableName string, table domain.Table, name, fkHint string, allTables map[string]domain.Table) []relCandidate {
	var out []relCandidate
	for _, field := range table.Fields {
		if field.ForeignKey == nil {
			continue
		}
		refTable, refCol, ok := fkTarget(field.ForeignKey.References, allTables)
		if !ok {
			continue
		}
		if fkHint != "" {
			if !matchesFKHint(fkHint, tableName, field.Name, refCol) || refTable != name {
				continue
			}
		} else if refTable != name && strings.TrimSuffix(field.Name, "_id") != name {
			continue
		}
		card := "many-to-one"
		if isOneToOne(table, field) {
			card = "one-to-one"
		}
		cons := fkConstraintName(tableName, field.Name)
		out = append(out, relCandidate{
			emb:          Embed{FKColumn: field.Name, RefTable: refTable, RefColumn: refCol},
			cardinality:  card,
			relationship: fmt.Sprintf("%s using %s(%s) and %s(%s)", cons, tableName, field.Name, refTable, refCol),
			constraint:   cons,
		})
	}
	return out
}

func hasManyRels(tableName, name, fkHint string, allTables map[string]domain.Table) []relCandidate {
	other, ok := allTables[name]
	if !ok || name == tableName {
		return nil
	}
	var out []relCandidate
	for _, field := range other.Fields {
		if field.ForeignKey == nil {
			continue
		}
		refTable, refCol, ok := fkTarget(field.ForeignKey.References, allTables)
		if !ok || refTable != tableName {
			continue
		}
		if fkHint != "" && !matchesFKHint(fkHint, name, field.Name, refCol) {
			continue
		}
		card := "one-to-many"
		if isOneToOne(other, field) {
			card = "one-to-one"
		}
		cons := fkConstraintName(name, field.Name)
		out = append(out, relCandidate{
			emb:          Embed{FKColumn: field.Name, RefTable: name, RefColumn: refCol, IsReverse: true},
			cardinality:  card,
			relationship: fmt.Sprintf("%s using %s(%s) and %s(%s)", cons, tableName, refCol, name, field.Name),
			constraint:   cons,
		})
	}
	return out
}

// junctionRels finds many-to-many paths: a table whose PK holds a non-unique FK to each side (PostgREST addM2MRels).
func junctionRels(tableName, name, fkHint string, allTables map[string]domain.Table) []relCandidate {
	if tableName == name {
		return nil
	}
	if _, ok := allTables[name]; !ok {
		return nil
	}
	var out []relCandidate
	for _, jt := range slices.Sorted(maps.Keys(allTables)) {
		if fkHint != "" && fkHint != jt {
			continue
		}
		fields := allTables[jt].Fields
		for _, src := range fields {
			srcTable, srcRef, ok := junctionLeg(src, allTables)
			if !ok || srcTable != tableName {
				continue
			}
			for _, tgt := range fields {
				tgtTable, tgtRef, ok := junctionLeg(tgt, allTables)
				if !ok || tgt.Name == src.Name || tgtTable != name {
					continue
				}
				out = append(out, relCandidate{
					emb: Embed{RefTable: name, IsReverse: true, Junction: &Junction{
						Table: jt, SourceColumn: src.Name, SourceRef: srcRef, TargetColumn: tgt.Name, TargetRef: tgtRef,
					}},
					cardinality: "many-to-many",
					relationship: fmt.Sprintf("%s using %s(%s) and %s(%s)",
						jt, fkConstraintName(jt, src.Name), src.Name, fkConstraintName(jt, tgt.Name), tgt.Name),
					constraint: jt,
				})
			}
		}
	}
	return out
}

func junctionLeg(f domain.Field, allTables map[string]domain.Table) (table, col string, ok bool) {
	if f.ForeignKey == nil || !f.PrimaryKey || f.Unique {
		return "", "", false
	}
	return fkTarget(f.ForeignKey.References, allTables)
}

var cardinalityRank = map[string]int{"one-to-many": 0, "many-to-one": 1, "one-to-one": 2, "many-to-many": 3}

// ambiguousEmbed lists candidates in PostgREST's derived Relationship order.
func ambiguousEmbed(parent, target string, cands []relCandidate) *AmbiguousEmbedError {
	slices.SortStableFunc(cands, func(a, b relCandidate) int {
		return cmp.Or(
			cmp.Compare(a.emb.RefTable, b.emb.RefTable),
			cmp.Compare(boolRank(a.emb.RefTable == parent), boolRank(b.emb.RefTable == parent)),
			cmp.Compare(cardinalityRank[a.cardinality], cardinalityRank[b.cardinality]),
			cmp.Compare(a.constraint, b.constraint),
			cmp.Compare(a.relationship, b.relationship))
	})
	e := &AmbiguousEmbedError{Parent: parent, Target: target}
	for _, c := range cands {
		e.Details = append(e.Details, map[string]string{
			"cardinality":  c.cardinality,
			"embedding":    parent + " with " + target,
			"relationship": c.relationship,
		})
		e.Hints = append(e.Hints, "'"+target+"!"+c.constraint+"'")
	}
	return e
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ToManyFrom is the FROM list and correlation of a to-many embed under parent.
func ToManyFrom(emb Embed, parent string) string {
	if j := emb.Junction; j != nil {
		return fmt.Sprintf("%s, %s WHERE %s.%s = %s.%s AND %s.%s = %s.%s",
			emb.RefTable, j.Table, emb.RefTable, j.TargetRef, j.Table, j.TargetColumn, j.Table, j.SourceColumn, parent, j.SourceRef)
	}
	return fmt.Sprintf("%s WHERE %s.%s = %s.%s", emb.RefTable, emb.RefTable, emb.FKColumn, parent, cmp.Or(emb.RefColumn, "id"))
}

// ToManyScope qualifies embed filters and order when a junction shares the FROM.
func ToManyScope(emb Embed) (*WhereNode, []OrderClause) {
	if emb.Junction == nil {
		return emb.Where, emb.Order
	}
	return AliasWhereColumns(emb.Where, emb.RefTable), QualifyOrderColumns(emb.Order, emb.RefTable)
}

// BuildEmbedRowExpr returns the JSON expression representing a single row of an embed.
func BuildEmbedRowExpr(emb Embed, srcAlias string, allTables map[string]domain.Table, argIdx int) (string, []any, int) {
	var allArgs []any

	if len(emb.Columns) == 0 && len(emb.Children) == 0 {
		return fmt.Sprintf("row_to_json(%s.*)", srcAlias), nil, argIdx
	}

	var entries []string

	scalarCols := emb.Columns
	if len(scalarCols) == 0 && len(emb.Children) > 0 {
		if allTables != nil {
			if refTbl, ok := allTables[emb.RefTable]; ok {
				for _, f := range refTbl.Fields {
					scalarCols = append(scalarCols, f.Name)
				}
				sort.Strings(scalarCols)
			}
		}
	}

	for _, c := range scalarCols {
		entries = append(entries, fmt.Sprintf("'%s', %s.%s", c, srcAlias, c))
	}

	for _, child := range emb.Children {
		childExpr, childArgs, nextIdx := BuildChildEmbedSubselect(child, srcAlias, allTables, argIdx)
		entries = append(entries, fmt.Sprintf("'%s', %s", child.OutputKey(), childExpr))
		allArgs = append(allArgs, childArgs...)
		argIdx = nextIdx
	}

	return fmt.Sprintf("json_build_object(%s)", strings.Join(entries, ", ")), allArgs, argIdx
}

// BuildChildEmbedSubselect builds a complete scalar subselect expression for a
// nested child embed.
func BuildChildEmbedSubselect(child Embed, parentAlias string, allTables map[string]domain.Table, argIdx int) (string, []any, int) {
	var allArgs []any

	if child.IsReverse {
		rowExpr, rowArgs, nextIdx := BuildEmbedRowExpr(child, child.RefTable, allTables, argIdx)
		allArgs = append(allArgs, rowArgs...)
		argIdx = nextIdx

		where, order := ToManyScope(child)
		sub := fmt.Sprintf("SELECT coalesce(json_agg(%s", rowExpr)
		if len(order) > 0 {
			sub += " ORDER BY " + RenderOrderBy(order)
		}
		sub += "), '[]'::json) FROM " + ToManyFrom(child, parentAlias)
		if where != nil {
			clauseSQL, clauseArgs, next := where.BuildSQL(argIdx)
			if clauseSQL != "" {
				sub += " AND " + clauseSQL
				allArgs = append(allArgs, clauseArgs...)
				argIdx = next
			}
		}
		if child.Limit != nil {
			sub += fmt.Sprintf(" LIMIT %d", *child.Limit)
		}
		if child.Offset != nil {
			sub += fmt.Sprintf(" OFFSET %d", *child.Offset)
		}
		return fmt.Sprintf("(%s)", sub), allArgs, argIdx
	}

	rowExpr, rowArgs, nextIdx := BuildEmbedRowExpr(child, child.RefTable, allTables, argIdx)
	allArgs = append(allArgs, rowArgs...)
	argIdx = nextIdx

	sub := fmt.Sprintf("SELECT %s FROM %s WHERE %s.%s = %s.%s LIMIT 1",
		rowExpr, child.RefTable, child.RefTable, child.RefColumn, parentAlias, child.FKColumn)
	return fmt.Sprintf("(%s)", sub), allArgs, argIdx
}

// BuildSelectQuery builds a SELECT SQL from QueryParams.
func BuildSelectQuery(tableName string, qp *QueryParams, table domain.Table) (string, []any) {
	return BuildSelectQueryFull(tableName, qp, table, nil)
}

// BuildSelectQueryFull builds a SELECT SQL from QueryParams with allTables for embed resolution.
func BuildSelectQueryFull(tableName string, qp *QueryParams, table domain.Table, allTables map[string]domain.Table) (string, []any) {
	var selectParts []string
	var groupByExprs []string
	var hasAgg bool
	if len(qp.Select) > 0 {
		var items []SelectItem
		for _, s := range qp.Select {
			if strings.Contains(s, "(") && !IsAggSelectEntry(s) {
				continue
			}
			item := ParseSelectItem(s)
			items = append(items, item)
			if item.Agg != "" {
				hasAgg = true
			}
		}
		for _, item := range items {
			selectParts = append(selectParts, RenderSelectItem(tableName, item))
			if hasAgg && item.Agg == "" {
				if expr := RenderSelectItemGroupByExpr(tableName, item); expr != "" {
					groupByExprs = append(groupByExprs, expr)
				}
			}
		}
	}
	if len(selectParts) == 0 {
		selectParts = append(selectParts, tableName+".*")
	}

	var allArgs []any
	argIdx := 1

	// With aggregates each embed output is a group key, as in PostgREST; jsonb makes it comparable.
	addGroupKey := func(part string) {
		selectParts = append(selectParts, part)
		if hasAgg {
			groupByExprs = append(groupByExprs, strconv.Itoa(len(selectParts)))
		}
	}
	addEmbedPart := func(expr, key string) {
		if hasAgg {
			expr = "(" + expr + ")::jsonb"
		}
		addGroupKey(expr + " AS " + key)
	}

	for _, emb := range qp.Embeds {
		alias := "_emb_" + emb.OutputKey()
		hasChildren := len(emb.Children) > 0
		if emb.IsReverse {
			rowExpr, rowArgs, nextIdx := BuildEmbedRowExpr(emb, emb.RefTable, allTables, argIdx)
			allArgs = append(allArgs, rowArgs...)
			argIdx = nextIdx

			where, order := ToManyScope(emb)
			needsInnerSubquery := emb.Limit != nil || emb.Offset != nil

			if needsInnerSubquery {
				cols := "*"
				if emb.Junction != nil {
					cols = emb.RefTable + ".*"
				}
				inner := "SELECT " + cols + " FROM " + ToManyFrom(emb, tableName)
				if where != nil {
					clauseSQL, clauseArgs, next := where.BuildSQL(argIdx)
					if clauseSQL != "" {
						inner += " AND " + clauseSQL
						allArgs = append(allArgs, clauseArgs...)
						argIdx = next
					}
				}
				if len(order) > 0 {
					inner += " ORDER BY " + RenderOrderBy(order)
				}
				if emb.Limit != nil {
					inner += fmt.Sprintf(" LIMIT %d", *emb.Limit)
				}
				if emb.Offset != nil {
					inner += fmt.Sprintf(" OFFSET %d", *emb.Offset)
				}
				sub := fmt.Sprintf(
					"SELECT coalesce(json_agg(%s), '[]'::json) FROM (%s) %s",
					rowExpr, inner, emb.RefTable)
				addEmbedPart("("+sub+")", emb.OutputKey())
			} else {
				sub := fmt.Sprintf(
					"SELECT coalesce(json_agg(%s", rowExpr)
				if len(order) > 0 {
					sub += " ORDER BY " + RenderOrderBy(order)
				}
				sub += "), '[]'::json) FROM " + ToManyFrom(emb, tableName)
				if where != nil {
					clauseSQL, clauseArgs, next := where.BuildSQL(argIdx)
					if clauseSQL != "" {
						sub += " AND " + clauseSQL
						allArgs = append(allArgs, clauseArgs...)
						argIdx = next
					}
				}
				addEmbedPart("("+sub+")", emb.OutputKey())
			}
		} else if emb.Spread {
			spreadCols := emb.Columns
			if len(spreadCols) == 0 {
				refTbl := allTables[emb.RefTable]
				for _, f := range refTbl.Fields {
					spreadCols = append(spreadCols, f.Name)
				}
				sort.Strings(spreadCols)
			}
			for _, c := range spreadCols {
				addGroupKey(alias + "." + c)
			}
			for _, child := range emb.Children {
				childExpr, childArgs, nextIdx := BuildChildEmbedSubselect(child, alias, allTables, argIdx)
				addEmbedPart(childExpr, child.OutputKey())
				allArgs = append(allArgs, childArgs...)
				argIdx = nextIdx
			}
		} else if hasChildren {
			rowExpr, rowArgs, nextIdx := BuildEmbedRowExpr(emb, alias, allTables, argIdx)
			allArgs = append(allArgs, rowArgs...)
			argIdx = nextIdx
			addEmbedPart(NullIfNoMatch(alias, emb.RefColumn, rowExpr), emb.OutputKey())
		} else {
			if len(emb.Columns) == 0 {
				addEmbedPart(fmt.Sprintf("row_to_json(%s.*)", alias), emb.OutputKey())
			} else {
				var embCols []string
				for _, c := range emb.Columns {
					embCols = append(embCols, fmt.Sprintf("'%s', %s.%s", c, alias, c))
				}
				obj := NullIfNoMatch(alias, emb.RefColumn, fmt.Sprintf("json_build_object(%s)", strings.Join(embCols, ", ")))
				addEmbedPart(obj, emb.OutputKey())
			}
		}
	}

	sql := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selectParts, ", "), tableName)

	for _, emb := range qp.Embeds {
		if emb.IsReverse {
			continue
		}
		alias := "_emb_" + emb.OutputKey()
		joinKind := "LEFT JOIN"
		if emb.Inner {
			joinKind = "INNER JOIN"
		}
		on := fmt.Sprintf("%s.%s = %s.%s", tableName, emb.FKColumn, alias, emb.RefColumn)
		if emb.Where != nil {
			if clauseSQL, clauseArgs, next := AliasWhereColumns(emb.Where, alias).BuildSQL(argIdx); clauseSQL != "" {
				on += " AND " + clauseSQL
				allArgs = append(allArgs, clauseArgs...)
				argIdx = next
			}
		}
		sql += fmt.Sprintf(" %s %s AS %s ON %s", joinKind, emb.RefTable, alias, on)
	}

	parentWhere := qp.Where
	if HasBelongsToJoin(qp.Embeds) {
		parentWhere = AliasWhereColumns(qp.Where, tableName)
	}
	whereSQL, whereArgs, nextArgIdx := parentWhere.BuildSQL(argIdx)
	if whereSQL != "" {
		allArgs = append(allArgs, whereArgs...)
	}
	argIdx = nextArgIdx
	var whereParts []string
	if whereSQL != "" {
		whereParts = append(whereParts, whereSQL)
	}

	for _, emb := range qp.Embeds {
		if !emb.IsReverse || !emb.Inner {
			continue
		}
		where, _ := ToManyScope(emb)
		existsSQL := "EXISTS (SELECT 1 FROM " + ToManyFrom(emb, tableName)
		if where != nil {
			clauseSQL, clauseArgs, next := where.BuildSQL(argIdx)
			if clauseSQL != "" {
				existsSQL += " AND " + clauseSQL
				allArgs = append(allArgs, clauseArgs...)
				argIdx = next
			}
		}
		existsSQL += ")"
		whereParts = append(whereParts, existsSQL)
	}

	if len(whereParts) > 0 {
		sql += " WHERE " + strings.Join(whereParts, " AND ")
	}

	if len(groupByExprs) > 0 {
		sql += " GROUP BY " + strings.Join(groupByExprs, ", ")
	}

	if qp.Having != nil {
		if havingSQL, havingArgs, _ := qp.Having.BuildSQL(argIdx); havingSQL != "" {
			sql += " HAVING " + havingSQL
			allArgs = append(allArgs, havingArgs...)
		}
	}

	if len(qp.Order) > 0 {
		order := qp.Order
		if HasBelongsToJoin(qp.Embeds) {
			order = QualifyOrderColumns(qp.Order, tableName)
		}
		sql += " ORDER BY " + RenderOrderBy(order)
	}

	if qp.Limit >= 0 {
		sql += fmt.Sprintf(" LIMIT %d OFFSET %d", qp.Limit, qp.Offset)
	} else if qp.Offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", qp.Offset)
	}

	return sql, allArgs
}

// FindUnknownFields returns field names not in the provided field map.
func FindUnknownFields(record map[string]any, fieldMap map[string]domain.Field) []string {
	var unknowns []string
	for key := range record {
		if _, ok := fieldMap[key]; !ok {
			unknowns = append(unknowns, key)
		}
	}
	sort.Strings(unknowns)
	return unknowns
}

// RecordsAllEmpty reports whether every record has no keys.
func RecordsAllEmpty(records []map[string]any) bool {
	for _, r := range records {
		if len(r) > 0 {
			return false
		}
	}
	return true
}

// UnionColumns returns the sorted union of keys across all records.
func UnionColumns(records []map[string]any) []string {
	set := map[string]bool{}
	for _, r := range records {
		for k := range r {
			set[k] = true
		}
	}
	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols
}

// RenderRowTuples builds the VALUES tuples for a list of records against a
// fixed column order.
func RenderRowTuples(records []map[string]any, cols []string, startArg int) ([]any, []string) {
	var args []any
	argIdx := startArg
	rowSQLs := make([]string, 0, len(records))
	for _, rec := range records {
		parts := make([]string, len(cols))
		for i, col := range cols {
			if v, ok := rec[col]; ok {
				parts[i] = fmt.Sprintf("$%d", argIdx)
				args = append(args, v)
				argIdx++
			} else {
				parts[i] = "DEFAULT"
			}
		}
		rowSQLs = append(rowSQLs, "("+strings.Join(parts, ", ")+")")
	}
	return args, rowSQLs
}

// BuildBulkInsertQuery emits a single INSERT statement covering every record.
func BuildBulkInsertQuery(tableName string, records []map[string]any, returning bool) (string, []any) {
	cols := UnionColumns(records)
	args, rowSQLs := RenderRowTuples(records, cols, 1)

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(rowSQLs, ", "))
	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

// BuildBulkUpsertQuery emits one multi-VALUES INSERT with an ON CONFLICT clause.
func BuildBulkUpsertQuery(tableName string, records []map[string]any, conflictCols []string, resolution string, returning bool) (string, []any) {
	cols := UnionColumns(records)
	args, rowSQLs := RenderRowTuples(records, cols, 1)

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s ON CONFLICT (%s) ",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(rowSQLs, ", "),
		strings.Join(conflictCols, ", "))

	if resolution == "ignore" {
		sql += "DO NOTHING"
	} else {
		conflictSet := make(map[string]bool, len(conflictCols))
		for _, c := range conflictCols {
			conflictSet[c] = true
		}
		var setParts []string
		for _, col := range cols {
			if conflictSet[col] {
				continue
			}
			setParts = append(setParts, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
		}
		if len(setParts) == 0 {
			sql += "DO NOTHING"
		} else {
			sql += "DO UPDATE SET " + strings.Join(setParts, ", ")
		}
	}
	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

// BuildInsertQuery emits an INSERT statement for a single record.
func BuildInsertQuery(tableName string, record map[string]any, returning bool) (string, []any) {
	cols := sortedMapKeys(record)
	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))

	for i, col := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = record[col]
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "))

	if returning {
		sql += " RETURNING *"
	}

	return sql, args
}

// ParseColumnsParam parses a "columns=a,b" query param into an allow-set.
func ParseColumnsParam(val string, table domain.Table) (map[string]bool, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}
	cols := map[string]bool{}
	for _, c := range strings.Split(val, ",") {
		c = strings.TrimSpace(c)
		if len(c) >= 2 && c[0] == '"' && c[len(c)-1] == '"' {
			c = c[1 : len(c)-1]
		}
		if c == "" {
			return nil, fmt.Errorf("empty column in columns hint")
		}
		if _, ok := table.GetField(c); !ok {
			return nil, fmt.Errorf("unknown column %q in columns hint", c)
		}
		cols[c] = true
	}
	return cols, nil
}

// FilterRecordsByColumns returns a copy of records where each record only
// retains keys present in allowed.
func FilterRecordsByColumns(records []map[string]any, allowed map[string]bool) []map[string]any {
	if allowed == nil {
		return records
	}
	out := make([]map[string]any, len(records))
	for i, rec := range records {
		filtered := make(map[string]any, len(rec))
		for k, v := range rec {
			if allowed[k] {
				filtered[k] = v
			}
		}
		out[i] = filtered
	}
	return out
}

// ParseOnConflictParam parses a "on_conflict=a,b" query param.
func ParseOnConflictParam(val string, table domain.Table) ([]string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}
	var cols []string
	for _, c := range strings.Split(val, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			return nil, fmt.Errorf("empty column in on_conflict")
		}
		if err := ValidateColumn(table, c); err != nil {
			return nil, fmt.Errorf("invalid on_conflict column: %w", err)
		}
		cols = append(cols, c)
	}
	return cols, nil
}

// PrimaryKeyColumns returns the PK field names for a table, sorted.
func PrimaryKeyColumns(table domain.Table) []string {
	var pks []string
	for _, f := range table.Fields {
		if f.PrimaryKey {
			pks = append(pks, f.Name)
		}
	}
	sort.Strings(pks)
	return pks
}

// BuildUpsertQuery emits INSERT ... ON CONFLICT (pk) DO {UPDATE|NOTHING}.
func BuildUpsertQuery(tableName string, record map[string]any, conflictCols []string, resolution string, returning bool) (string, []any) {
	cols := sortedMapKeys(record)
	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))

	for i, col := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = record[col]
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) ",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(conflictCols, ", "))

	if resolution == "ignore" {
		sql += "DO NOTHING"
	} else {
		conflictSet := make(map[string]bool, len(conflictCols))
		for _, c := range conflictCols {
			conflictSet[c] = true
		}
		var setParts []string
		for _, col := range cols {
			if conflictSet[col] {
				continue
			}
			setParts = append(setParts, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
		}
		if len(setParts) == 0 {
			sql += "DO NOTHING"
		} else {
			sql += "DO UPDATE SET " + strings.Join(setParts, ", ")
		}
	}

	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

// BuildUpdateQuery emits an UPDATE statement.
func BuildUpdateQuery(tableName string, updates map[string]any, where *WhereNode, returning bool) (string, []any) {
	cols := sortedMapKeys(updates)
	var args []any
	argIdx := 1

	setParts := make([]string, len(cols))
	for i, col := range cols {
		setParts[i] = fmt.Sprintf("%s = $%d", col, argIdx)
		args = append(args, updates[col])
		argIdx++
	}

	sql := fmt.Sprintf("UPDATE %s SET %s", tableName, strings.Join(setParts, ", "))

	whereSQL, whereArgs, _ := where.BuildSQL(argIdx)
	if whereSQL != "" {
		sql += " WHERE " + whereSQL
		args = append(args, whereArgs...)
	}

	if returning {
		sql += " RETURNING *"
	}

	return sql, args
}

// BuildDeleteQuery emits a DELETE statement.
func BuildDeleteQuery(tableName string, where *WhereNode, returning bool) (string, []any) {
	sql := fmt.Sprintf("DELETE FROM %s", tableName)
	whereSQL, args, _ := where.BuildSQL(1)
	if whereSQL != "" {
		sql += " WHERE " + whereSQL
	}
	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

// String returns a string representation of QueryParams.
func (c *QueryParams) String() string {
	return fmt.Sprintf("select=%v where=%v order=%v limit=%d offset=%d",
		c.Select, c.Where != nil, c.Order, c.Limit, c.Offset)
}

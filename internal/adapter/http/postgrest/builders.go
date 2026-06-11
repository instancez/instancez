package postgrest

import (
	"fmt"
	"sort"
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

// ResolveEmbeds resolves embed names to FK relationships using the table config.
func ResolveEmbeds(tableName string, table domain.Table, embedNames []string, allTables map[string]domain.Table) ([]Embed, error) {
	var embeds []Embed

	for _, raw := range embedNames {
		name, alias, cols, nested, spread := ParseEmbedParam(raw)
		name, inner, fkHint := ParseEmbedHint(name)
		if spread && alias != "" {
			return nil, fmt.Errorf("alias not allowed on spread embed %q", alias+":"+name)
		}

		found := false
		for _, field := range table.Fields {
			if field.ForeignKey == nil {
				continue
			}
			fieldName := field.Name
			ref := field.ForeignKey.References
			parts := strings.SplitN(ref, ".", 2)
			if len(parts) != 2 {
				continue
			}
			refTable, refCol := parts[0], parts[1]

			if fkHint != "" {
				if fieldName != fkHint && strings.TrimSuffix(fieldName, "_id") != fkHint {
					continue
				}
				if refTable != name {
					continue
				}
			} else if !(refTable == name || strings.TrimSuffix(fieldName, "_id") == name) {
				continue
			}
			emb := Embed{
				Name:      name,
				Alias:     alias,
				Columns:   cols,
				FKColumn:  fieldName,
				RefTable:  refTable,
				RefColumn: refCol,
				Inner:     inner,
				Spread:    spread,
			}
			if len(nested) > 0 {
				refTbl, ok := allTables[refTable]
				if !ok {
					return nil, fmt.Errorf("embed %q references unknown table %q", name, refTable)
				}
				children, err := ResolveEmbeds(refTable, refTbl, nested, allTables)
				if err != nil {
					return nil, fmt.Errorf("nested embed in %q: %w", name, err)
				}
				emb.Children = children
			}
			embeds = append(embeds, emb)
			found = true
			break
		}

		if found {
			continue
		}

		for otherName, otherTable := range allTables {
			if otherName == tableName {
				continue
			}
			if otherName != name {
				continue
			}
			if spread {
				return nil, fmt.Errorf("spread (...) not allowed on has-many embed %q", name)
			}
			for _, field := range otherTable.Fields {
				if field.ForeignKey == nil {
					continue
				}
				fieldName := field.Name
				ref := field.ForeignKey.References
				parts := strings.SplitN(ref, ".", 2)
				if len(parts) != 2 {
					continue
				}
				if parts[0] == tableName {
					if fkHint != "" && fieldName != fkHint && strings.TrimSuffix(fieldName, "_id") != fkHint {
						continue
					}
					emb := Embed{
						Name:      name,
						Alias:     alias,
						Columns:   cols,
						FKColumn:  fieldName,
						RefTable:  otherName,
						RefColumn: parts[1],
						IsReverse: true,
						Inner:     inner,
					}
					if len(nested) > 0 {
						children, err := ResolveEmbeds(otherName, otherTable, nested, allTables)
						if err != nil {
							return nil, fmt.Errorf("nested embed in %q: %w", name, err)
						}
						emb.Children = children
					}
					embeds = append(embeds, emb)
					found = true
					break
				}
			}
		}

		if !found {
			return nil, fmt.Errorf("could not find a relationship between %q and %q in the schema", tableName, name)
		}
	}

	return embeds, nil
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

		refPK := child.RefColumn
		if refPK == "" {
			refPK = "id"
		}
		sub := fmt.Sprintf("SELECT coalesce(json_agg(%s", rowExpr)
		if len(child.Order) > 0 {
			sub += " ORDER BY " + RenderOrderBy(child.Order)
		}
		sub += fmt.Sprintf("), '[]'::json) FROM %s WHERE %s.%s = %s.%s",
			child.RefTable, child.RefTable, child.FKColumn, parentAlias, refPK)
		if child.Where != nil {
			clauseSQL, clauseArgs, next := child.Where.BuildSQL(argIdx)
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
		if len(table.Searchable) > 0 {
			for _, f := range table.Fields {
				selectParts = append(selectParts, tableName+"."+f.Name)
			}
		} else {
			selectParts = append(selectParts, tableName+".*")
		}
	}

	var allArgs []any
	argIdx := 1
	var belongsToWhere []string

	for _, emb := range qp.Embeds {
		alias := "_emb_" + emb.OutputKey()
		hasChildren := len(emb.Children) > 0
		if emb.IsReverse {
			rowExpr, rowArgs, nextIdx := BuildEmbedRowExpr(emb, emb.RefTable, allTables, argIdx)
			allArgs = append(allArgs, rowArgs...)
			argIdx = nextIdx

			refPK := emb.RefColumn
			if refPK == "" {
				refPK = "id"
			}

			needsInnerSubquery := emb.Limit != nil || emb.Offset != nil

			if needsInnerSubquery {
				inner := fmt.Sprintf("SELECT * FROM %s WHERE %s.%s = %s.%s",
					emb.RefTable, emb.RefTable, emb.FKColumn, tableName, refPK)
				if emb.Where != nil {
					clauseSQL, clauseArgs, next := emb.Where.BuildSQL(argIdx)
					if clauseSQL != "" {
						inner += " AND " + clauseSQL
						allArgs = append(allArgs, clauseArgs...)
						argIdx = next
					}
				}
				if len(emb.Order) > 0 {
					inner += " ORDER BY " + RenderOrderBy(emb.Order)
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
				selectParts = append(selectParts, fmt.Sprintf("(%s) AS %s", sub, emb.OutputKey()))
			} else {
				sub := fmt.Sprintf(
					"SELECT coalesce(json_agg(%s", rowExpr)
				if len(emb.Order) > 0 {
					sub += " ORDER BY " + RenderOrderBy(emb.Order)
				}
				sub += fmt.Sprintf("), '[]'::json) FROM %s WHERE %s.%s = %s.%s",
					emb.RefTable, emb.RefTable, emb.FKColumn, tableName, refPK)
				if emb.Where != nil {
					clauseSQL, clauseArgs, next := emb.Where.BuildSQL(argIdx)
					if clauseSQL != "" {
						sub += " AND " + clauseSQL
						allArgs = append(allArgs, clauseArgs...)
						argIdx = next
					}
				}
				selectParts = append(selectParts, fmt.Sprintf("(%s) AS %s", sub, emb.OutputKey()))
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
				selectParts = append(selectParts, fmt.Sprintf("%s.%s", alias, c))
			}
			for _, child := range emb.Children {
				childExpr, childArgs, nextIdx := BuildChildEmbedSubselect(child, alias, allTables, argIdx)
				selectParts = append(selectParts, fmt.Sprintf("%s AS %s", childExpr, child.OutputKey()))
				allArgs = append(allArgs, childArgs...)
				argIdx = nextIdx
			}
		} else if hasChildren {
			rowExpr, rowArgs, nextIdx := BuildEmbedRowExpr(emb, alias, allTables, argIdx)
			allArgs = append(allArgs, rowArgs...)
			argIdx = nextIdx
			selectParts = append(selectParts, fmt.Sprintf("%s AS %s", rowExpr, emb.OutputKey()))
		} else {
			if len(emb.Columns) == 0 {
				selectParts = append(selectParts,
					fmt.Sprintf("row_to_json(%s.*) AS %s", alias, emb.OutputKey()))
			} else {
				var embCols []string
				for _, c := range emb.Columns {
					embCols = append(embCols, fmt.Sprintf("'%s', %s.%s", c, alias, c))
				}
				selectParts = append(selectParts,
					fmt.Sprintf("json_build_object(%s) AS %s", strings.Join(embCols, ", "), emb.OutputKey()))
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
		sql += fmt.Sprintf(" %s %s AS %s ON %s.%s = %s.%s",
			joinKind, emb.RefTable, alias, tableName, emb.FKColumn, alias, emb.RefColumn)
		if emb.Where != nil {
			clauseSQL, clauseArgs, next := AliasWhereColumns(emb.Where, alias).BuildSQL(argIdx)
			if clauseSQL != "" {
				belongsToWhere = append(belongsToWhere, clauseSQL)
				allArgs = append(allArgs, clauseArgs...)
				argIdx = next
			}
		}
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
	whereParts = append(whereParts, belongsToWhere...)

	for _, emb := range qp.Embeds {
		if !emb.IsReverse || !emb.Inner {
			continue
		}
		refPK := emb.RefColumn
		if refPK == "" {
			refPK = "id"
		}
		existsSQL := fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s.%s = %s.%s",
			emb.RefTable, emb.RefTable, emb.FKColumn, tableName, refPK)
		if emb.Where != nil {
			clauseSQL, clauseArgs, next := emb.Where.BuildSQL(argIdx)
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
		havingSQL, havingArgs, nextIdx := qp.Having.BuildSQL(argIdx)
		if havingSQL != "" {
			sql += " HAVING " + havingSQL
			allArgs = append(allArgs, havingArgs...)
			argIdx = nextIdx
		}
	}

	if len(qp.Order) > 0 {
		order := qp.Order
		if HasBelongsToJoin(qp.Embeds) {
			order = QualifyOrderColumns(qp.Order, tableName)
		}
		sql += " ORDER BY " + RenderOrderBy(order)
	}

	sql += fmt.Sprintf(" LIMIT %d OFFSET %d", qp.Limit, qp.Offset)

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

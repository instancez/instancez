package postgrest

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// WhereNode is a node in the filter expression tree. A node is either a leaf
// (wrapping a single Filter) or an internal AND/OR node with children.
// The root node produced by ParseWhere is always an AND over the top-level
// query parameters — an implicit conjunction.
type WhereNode struct {
	Op       string       // "and", "or", or "" for leaf
	Not      bool         // whether the node is negated
	Leaf     *Filter      // set when this is a leaf node
	Children []*WhereNode // set for internal nodes
}

// IsLeaf reports whether n is a leaf node.
func (n *WhereNode) IsLeaf() bool { return n != nil && n.Leaf != nil }

// AndLeaves builds a flat AND node from a sequence of leaf filters.
// Convenience for tests and code that doesn't need nesting.
func AndLeaves(filters ...Filter) *WhereNode {
	if len(filters) == 0 {
		return nil
	}
	n := &WhereNode{Op: "and"}
	for i := range filters {
		f := filters[i]
		n.Children = append(n.Children, &WhereNode{Leaf: &f})
	}
	return n
}

// BuildSQL emits a SQL boolean expression (with $N placeholders) starting
// at argIdx. Returns the expression string, its args, and the next free
// placeholder index. An empty tree returns "".
func (n *WhereNode) BuildSQL(argIdx int) (string, []any, int) {
	if n == nil {
		return "", nil, argIdx
	}

	if n.Leaf != nil {
		sql, args, next := buildFilterCondition(*n.Leaf, argIdx)
		if n.Not {
			sql = "NOT (" + sql + ")"
		}
		return sql, args, next
	}

	var parts []string
	var args []any
	for _, c := range n.Children {
		s, a, next := c.BuildSQL(argIdx)
		if s == "" {
			continue
		}
		parts = append(parts, s)
		args = append(args, a...)
		argIdx = next
	}
	if len(parts) == 0 {
		return "", nil, argIdx
	}
	sep := " AND "
	if n.Op == "or" {
		sep = " OR "
	}
	sql := strings.Join(parts, sep)
	if len(parts) > 1 {
		sql = "(" + sql + ")"
	}
	if n.Not {
		sql = "NOT " + sql
	}
	return sql, args, argIdx
}

// ValidateColumn ensures col references a field declared on the table.
// JSONB access expressions (e.g. "metadata->>theme") are accepted when the
// base column exists and the path key is a safe identifier.
func ValidateColumn(table domain.Table, col string) error {
	if col == "" {
		return fmt.Errorf("empty column name")
	}
	base, steps := splitJSONBPath(col)
	if _, ok := table.GetField(base); !ok {
		return fmt.Errorf("unknown column %q", base)
	}
	for _, s := range steps {
		if s.key == "" {
			return fmt.Errorf("empty JSONB key in %q", col)
		}
		if !s.isInt && !jsonKeyRe.MatchString(s.key) {
			return fmt.Errorf("invalid JSONB key %q", s.key)
		}
	}
	return nil
}

// reservedParams are non-filter query parameters. The or/and keys are not
// listed here because they are handled explicitly in ParseWhere's switch.
var reservedParams = map[string]bool{
	"select": true,
	"order":  true,
	"limit":  true,
	"offset": true,
	"having": true,
}

// jsonKeyRe restricts JSONB path keys to safe identifiers.
var jsonKeyRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// identRe matches a safe SQL identifier (alias or cast type).
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validOps maps PostgREST operator names to SQL operators.
var validOps = map[string]string{
	"eq":         "=",
	"neq":        "!=",
	"gt":         ">",
	"gte":        ">=",
	"lt":         "<",
	"lte":        "<=",
	"like":       "LIKE",
	"ilike":      "ILIKE",
	"match":      "~",
	"imatch":     "~*",
	"is":         "IS",
	"isdistinct": "IS DISTINCT FROM",
	"in":         "IN",
	"cs":         "@>",
	"cd":         "<@",
	"ov":         "&&",
	"fts":        "@@",
	"plfts":      "@@",
	"phfts":      "@@",
	"wfts":       "@@",
	"sl":         "<<",
	"sr":         ">>",
	"nxl":        "&>",
	"nxr":        "&<",
	"adj":        "-|-",
	"like(all)":  "LIKE",
	"like(any)":  "LIKE",
	"ilike(all)": "ILIKE",
	"ilike(any)": "ILIKE",
}

// ftsOps are the full-text-search operators that require a text-like or
// tsvector target column.
var ftsOps = map[string]bool{
	"fts":   true,
	"plfts": true,
	"phfts": true,
	"wfts":  true,
}

// aggNames is the set of PostgREST-style aggregate suffixes.
var aggNames = []string{"count", "sum", "avg", "min", "max"}

// selectItem is a parsed non-embed entry from the select query param.
type selectItem struct {
	Alias string
	Col   string
	Cast  string
	Agg   string
}

// parseSelectItemInternal splits a select entry into alias, column, cast, and aggregate parts.
func parseSelectItemInternal(s string) selectItem {
	item := selectItem{}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ':' {
				break
			}
			item.Alias = s[:i]
			s = s[i+1:]
			break
		}
	}
	if idx := strings.Index(s, "::"); idx != -1 {
		item.Cast = s[idx+2:]
		s = s[:idx]
	}
	for _, agg := range aggNames {
		suffix := "." + agg + "()"
		if strings.HasSuffix(s, suffix) {
			item.Agg = agg
			s = s[:len(s)-len(suffix)]
			break
		}
		if s == agg+"()" {
			item.Agg = agg
			s = ""
			break
		}
	}
	item.Col = s
	return item
}

// isAggSelectEntryInternal reports whether a raw select entry is an aggregate.
func isAggSelectEntryInternal(raw string) bool {
	s := raw
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ':' {
				break
			}
			s = s[i+1:]
			break
		}
	}
	if idx := strings.Index(s, "::"); idx != -1 {
		s = s[:idx]
	}
	for _, agg := range aggNames {
		if s == agg+"()" || strings.HasSuffix(s, "."+agg+"()") {
			return true
		}
	}
	return false
}

// splitJSONBPath parses a filter/select column like "data->items->0->>name"
// into the base column ("data") and an ordered list of JSONB access steps.
func splitJSONBPath(col string) (string, []jsonPathStep) {
	idx := strings.Index(col, "->")
	if idx == -1 {
		return col, nil
	}
	base := col[:idx]
	rest := col[idx:]
	var steps []jsonPathStep
	for len(rest) > 0 {
		if !strings.HasPrefix(rest, "->") {
			break
		}
		op := "->"
		keyStart := 2
		if len(rest) > 2 && rest[2] == '>' {
			op = "->>"
			keyStart = 3
		}
		tail := rest[keyStart:]
		next := strings.Index(tail, "->")
		var key string
		if next == -1 {
			key = tail
			rest = ""
		} else {
			key = tail[:next]
			rest = tail[next:]
		}
		steps = append(steps, jsonPathStep{op: op, key: key, isInt: isAllDigits(key)})
	}
	return base, steps
}

// renderJSONBSuffix serializes a JSONB access chain onto a prefix.
func renderJSONBSuffix(steps []jsonPathStep) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.op)
		if s.isInt {
			b.WriteString(s.key)
		} else {
			b.WriteByte('\'')
			b.WriteString(s.key)
			b.WriteByte('\'')
		}
	}
	return b.String()
}

// lastJSONBOp reports the operator of the final step in a JSONB chain.
func lastJSONBOp(steps []jsonPathStep) string {
	if len(steps) == 0 {
		return ""
	}
	return steps[len(steps)-1].op
}

// isAllDigits reports whether s consists entirely of ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseFilterValue splits "eq.active" into ("eq", "active", "", nil).
func parseFilterValue(val string) (op, operand, config string, err error) {
	idx := strings.Index(val, ".")
	if idx == -1 {
		return "", "", "", fmt.Errorf("expected operator.value format, got %q", val)
	}
	op = val[:idx]
	operand = val[idx+1:]

	if lp := strings.Index(op, "("); lp != -1 {
		if !strings.HasSuffix(op, ")") {
			return "", "", "", fmt.Errorf("malformed operator %q", op)
		}
		config = op[lp+1 : len(op)-1]
		op = op[:lp]
		if config == "" {
			return "", "", "", fmt.Errorf("empty config in operator %q", val[:idx])
		}
		if !identRe.MatchString(config) {
			return "", "", "", fmt.Errorf("invalid config %q", config)
		}
	}

	if _, ok := validOps[op]; !ok {
		return "", "", "", fmt.Errorf("unknown operator %q", op)
	}

	if op == "is" {
		switch strings.ToLower(strings.TrimSpace(operand)) {
		case "null", "true", "false", "unknown":
		default:
			return "", "", "", fmt.Errorf(`operator "is" only accepts null, true, false, or unknown`)
		}
	}

	return op, operand, config, nil
}

// parsePatternList parses the value portion of a like(all)/like(any) filter.
func parsePatternList(raw string) []string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		if (s[0] == '{' && s[len(s)-1] == '}') || (s[0] == '(' && s[len(s)-1] == ')') {
			s = s[1 : len(s)-1]
		}
	}
	if s == "" {
		return []string{""}
	}
	parts := strings.Split(s, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// buildFilterCondition emits a SQL boolean expression for a single Filter.
func buildFilterCondition(f Filter, argIdx int) (string, []any, int) {
	colExpr := f.Column
	baseCol, steps := splitJSONBPath(f.Column)
	if len(steps) > 0 {
		colExpr = baseCol + renderJSONBSuffix(steps)
	}

	switchOp := f.Operator
	if f.Config == "all" || f.Config == "any" {
		switchOp = f.Operator + "(" + f.Config + ")"
	}

	switch switchOp {
	case "is":
		isVal := strings.ToUpper(strings.TrimSpace(f.Value))
		switch isVal {
		case "NULL", "TRUE", "FALSE", "UNKNOWN":
			return fmt.Sprintf("%s IS %s", colExpr, isVal), nil, argIdx
		default:
			return fmt.Sprintf("%s IS NOT DISTINCT FROM $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1
		}

	case "isdistinct":
		val := strings.ToUpper(f.Value)
		if val == "NULL" || val == "TRUE" || val == "FALSE" {
			return fmt.Sprintf("%s IS DISTINCT FROM %s", colExpr, val), nil, argIdx
		}
		return fmt.Sprintf("%s IS DISTINCT FROM $%d", colExpr, argIdx),
			[]any{f.Value}, argIdx + 1

	case "in":
		inner := strings.TrimPrefix(f.Value, "(")
		inner = strings.TrimSuffix(inner, ")")
		vals := strings.Split(inner, ",")
		placeholders := make([]string, len(vals))
		var args []any
		for i, v := range vals {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, strings.TrimSpace(v))
			argIdx++
		}
		return fmt.Sprintf("%s IN (%s)", colExpr, strings.Join(placeholders, ", ")), args, argIdx

	case "fts", "plfts", "phfts", "wfts":
		fn := map[string]string{
			"fts":   "to_tsquery",
			"plfts": "plainto_tsquery",
			"phfts": "phraseto_tsquery",
			"wfts":  "websearch_to_tsquery",
		}[f.Operator]
		if f.Config != "" {
			return fmt.Sprintf("%s @@ %s('%s', $%d)", colExpr, fn, f.Config, argIdx),
				[]any{f.Value}, argIdx + 1
		}
		return fmt.Sprintf("%s @@ %s($%d)", colExpr, fn, argIdx), []any{f.Value}, argIdx + 1

	case "cs":
		return fmt.Sprintf("%s @> $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1
	case "cd":
		return fmt.Sprintf("%s <@ $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1
	case "ov":
		return fmt.Sprintf("%s && $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1

	case "sl", "sr", "nxl", "nxr", "adj":
		return fmt.Sprintf("%s %s $%d", colExpr, validOps[f.Operator], argIdx),
			[]any{f.Value}, argIdx + 1

	case "like(all)", "like(any)", "ilike(all)", "ilike(any)":
		sqlOp := "LIKE"
		if strings.HasPrefix(switchOp, "ilike") {
			sqlOp = "ILIKE"
		}
		quant := "ALL"
		if strings.HasSuffix(switchOp, "(any)") {
			quant = "ANY"
		}
		patterns := parsePatternList(f.Value)
		placeholders := make([]string, len(patterns))
		args := make([]any, len(patterns))
		for i, p := range patterns {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args[i] = p
			argIdx++
		}
		return fmt.Sprintf("%s %s %s(ARRAY[%s])",
			colExpr, sqlOp, quant, strings.Join(placeholders, ", ")), args, argIdx

	default:
		sqlOp := validOps[f.Operator]
		return fmt.Sprintf("%s %s $%d", colExpr, sqlOp, argIdx), []any{f.Value}, argIdx + 1
	}
}

// IsFTSCompatibleType reports whether a field's declared type can be the
// left-hand operand of an FTS operator (fts/plfts/phfts/wfts).
func IsFTSCompatibleType(t string) bool {
	return isFTSCompatibleType(t)
}

// isFTSCompatibleType is the internal implementation.
func isFTSCompatibleType(t string) bool {
	s := strings.ToLower(strings.TrimSpace(t))
	if idx := strings.Index(s, "("); idx != -1 {
		s = strings.TrimSpace(s[:idx])
	}
	switch s {
	case "text", "citext", "tsvector",
		"varchar", "character varying",
		"char", "character",
		"bpchar":
		return true
	}
	return false
}

// ValidateFTSLeaves walks a parsed WHERE tree and rejects any leaf that
// applies an FTS operator to a column whose declared type isn't text-like
// or tsvector.
func ValidateFTSLeaves(n *WhereNode, table domain.Table) error {
	return validateFTSLeaves(n, table)
}

// validateFTSLeaves is the internal implementation.
func validateFTSLeaves(n *WhereNode, table domain.Table) error {
	if n == nil {
		return nil
	}
	if n.Leaf != nil {
		if !ftsOps[n.Leaf.Operator] {
			return nil
		}
		base, steps := splitJSONBPath(n.Leaf.Column)
		if last := lastJSONBOp(steps); last == "->>" {
			return nil
		} else if last == "->" {
			return fmt.Errorf("operator %q requires a text or tsvector column; %q yields jsonb",
				n.Leaf.Operator, n.Leaf.Column)
		}
		field, ok := table.GetField(base)
		if !ok {
			return fmt.Errorf("unknown column %q", base)
		}
		if !isFTSCompatibleType(field.Type) {
			return fmt.Errorf("operator %q requires a text or tsvector column; %q is %q",
				n.Leaf.Operator, base, field.Type)
		}
		return nil
	}
	for _, c := range n.Children {
		if err := validateFTSLeaves(c, table); err != nil {
			return err
		}
	}
	return nil
}

// ParseWhere builds a filter tree from the query string values. Top-level
// parameters are ANDed together.
func ParseWhere(vals url.Values, tableName string, table domain.Table) (*WhereNode, error) {
	return ParseWhereSkip(vals, tableName, table, nil)
}

// ParseWhereSkip is ParseWhere but skips query keys that start with any of
// the given prefixes followed by a dot.
func ParseWhereSkip(vals url.Values, tableName string, table domain.Table, skipPrefixes map[string]bool) (*WhereNode, error) {
	validate := func(col string) error { return ValidateColumn(table, col) }
	tree, err := ParseWhereWith(vals, validate, skipPrefixes, nil)
	if err != nil {
		return nil, err
	}
	if err := validateFTSLeaves(tree, table); err != nil {
		return nil, err
	}
	return tree, nil
}

// ParseWhereWith is the lower-level filter parser. It accepts a ColValidator
// callback instead of a concrete domain.Table so RPC call sites — which may
// have no corresponding table at all — can supply their own validation strategy.
// skipKeys is an exact-match skip set.
func ParseWhereWith(vals url.Values, validate ColValidator, skipPrefixes map[string]bool, skipKeys map[string]bool) (*WhereNode, error) {
	root := &WhereNode{Op: "and"}

	for key, values := range vals {
		if reservedParams[key] {
			continue
		}
		if skipKeys[key] {
			continue
		}
		if len(skipPrefixes) > 0 {
			skip := false
			for p := range skipPrefixes {
				if strings.HasPrefix(key, p+".") {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		switch key {
		case "or", "and":
			for _, v := range values {
				node, err := parseLogicListWith(key, v, validate)
				if err != nil {
					return nil, fmt.Errorf("invalid %s: %w", key, err)
				}
				root.Children = append(root.Children, node)
			}
		default:
			if err := validate(key); err != nil {
				return nil, fmt.Errorf("invalid filter on %q: %w", key, err)
			}
			for _, v := range values {
				leaf, err := parseLeafValue(key, v)
				if err != nil {
					return nil, fmt.Errorf("invalid filter on %q: %w", key, err)
				}
				root.Children = append(root.Children, leaf)
			}
		}
	}

	if len(root.Children) == 0 {
		return nil, nil
	}
	return root, nil
}

// ParseLeafValue parses a single filter value for a column param.
// Exported for use by parseEmbedScopedParams in package http (Task 5 moves it).
func ParseLeafValue(col, val string) (*WhereNode, error) {
	return parseLeafValue(col, val)
}

// parseLeafValue is the internal implementation.
func parseLeafValue(col, val string) (*WhereNode, error) {
	not := false
	if strings.HasPrefix(val, "not.") {
		not = true
		val = strings.TrimPrefix(val, "not.")
	}
	op, operand, config, err := parseFilterValue(val)
	if err != nil {
		return nil, err
	}
	return &WhereNode{
		Leaf: &Filter{Column: col, Operator: op, Value: operand, Config: config},
		Not:  not,
	}, nil
}

// ParseLogicListWith parses a value like "(a.eq.1,b.eq.2,and(c.eq.3,d.eq.4))"
// associated with top-level param "or" or "and".
// Exported for use by parseEmbedScopedParams in package http (Task 5 moves it).
func ParseLogicListWith(op, raw string, validate ColValidator) (*WhereNode, error) {
	return parseLogicListWith(op, raw, validate)
}

// parseLogicListWith is the internal implementation.
func parseLogicListWith(op, raw string, validate ColValidator) (*WhereNode, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "(") || !strings.HasSuffix(raw, ")") {
		return nil, fmt.Errorf("expected parenthesized list, got %q", raw)
	}
	inner := raw[1 : len(raw)-1]
	items, err := splitTopLevel(inner, ',')
	if err != nil {
		return nil, err
	}
	node := &WhereNode{Op: op}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("empty item in logic list")
		}
		child, err := parseLogicItemWith(item, validate)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
	}
	if len(node.Children) == 0 {
		return nil, fmt.Errorf("empty %s list", op)
	}
	return node, nil
}

// parseLogicItemWith parses one element of a logic list.
func parseLogicItemWith(item string, validate ColValidator) (*WhereNode, error) {
	not := false
	if strings.HasPrefix(item, "not.") {
		rest := strings.TrimPrefix(item, "not.")
		if strings.HasPrefix(rest, "and(") || strings.HasPrefix(rest, "or(") {
			not = true
			item = rest
		}
	}
	if strings.HasPrefix(item, "and(") || strings.HasPrefix(item, "or(") {
		op := "and"
		if strings.HasPrefix(item, "or(") {
			op = "or"
		}
		if !strings.HasSuffix(item, ")") {
			return nil, fmt.Errorf("unclosed %s group", op)
		}
		inner := item[len(op)+1 : len(item)-1]
		list, err := splitTopLevel(inner, ',')
		if err != nil {
			return nil, err
		}
		node := &WhereNode{Op: op, Not: not}
		for _, sub := range list {
			sub = strings.TrimSpace(sub)
			child, err := parseLogicItemWith(sub, validate)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
		}
		if len(node.Children) == 0 {
			return nil, fmt.Errorf("empty %s group", op)
		}
		return node, nil
	}

	return parseLogicLeafWith(item, validate)
}

// parseLogicLeafWith parses "col.op.val" or "col.not.op.val" into a leaf node.
func parseLogicLeafWith(item string, validate ColValidator) (*WhereNode, error) {
	firstDot := strings.Index(item, ".")
	if firstDot == -1 {
		return nil, fmt.Errorf("expected col.op.val, got %q", item)
	}
	col := item[:firstDot]
	rest := item[firstDot+1:]
	if err := validate(col); err != nil {
		return nil, err
	}
	not := false
	if strings.HasPrefix(rest, "not.") {
		not = true
		rest = strings.TrimPrefix(rest, "not.")
	}
	op, val, config, err := parseFilterValue(rest)
	if err != nil {
		return nil, err
	}
	return &WhereNode{
		Leaf: &Filter{Column: col, Operator: op, Value: val, Config: config},
		Not:  not,
	}, nil
}

// ParseHavingParam parses a having= query parameter into a WhereNode tree.
func ParseHavingParam(raw string, tableName string, table domain.Table, selectItems []string) (*WhereNode, error) {
	aggAliasExprs := map[string]string{}
	for _, s := range selectItems {
		if !isAggSelectEntryInternal(s) {
			continue
		}
		item := parseSelectItemInternal(s)
		alias := item.Alias
		if alias == "" && item.Agg != "" {
			alias = item.Agg
		}
		if alias == "" {
			continue
		}
		var expr string
		if item.Agg != "" && item.Col == "" {
			expr = "COUNT(*)"
		} else {
			base, steps := splitJSONBPath(item.Col)
			expr = fmt.Sprintf("%s.%s", tableName, base)
			if len(steps) > 0 {
				expr += renderJSONBSuffix(steps)
			}
			expr = fmt.Sprintf("%s(%s)", strings.ToUpper(item.Agg), expr)
		}
		if item.Cast != "" {
			expr = fmt.Sprintf("(%s)::%s", expr, item.Cast)
		}
		aggAliasExprs[alias] = expr
	}

	validate := func(col string) error {
		if _, ok := aggAliasExprs[col]; ok {
			return nil
		}
		if err := ValidateColumn(table, col); err == nil {
			return nil
		}
		return fmt.Errorf("column %q is not a valid aggregate alias or table column", col)
	}

	items, err := splitTopLevel(raw, ',')
	if err != nil {
		return nil, err
	}
	root := &WhereNode{Op: "and"}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		leaf, err := parseLogicLeafWith(item, validate)
		if err != nil {
			return nil, err
		}
		if leaf.Leaf != nil {
			if expr, ok := aggAliasExprs[leaf.Leaf.Column]; ok {
				leaf.Leaf.Column = expr
			} else {
				leaf.Leaf.Column = tableName + "." + leaf.Leaf.Column
			}
		}
		root.Children = append(root.Children, leaf)
	}
	if len(root.Children) == 0 {
		return nil, nil
	}
	return root, nil
}

// SplitTopLevel splits s on sep at paren depth 0. Exported for use by
// parseEmbedParam in package http (Task 5 moves it).
func SplitTopLevel(s string, sep rune) ([]string, error) {
	return splitTopLevel(s, sep)
}

// splitTopLevel is the internal implementation.
func splitTopLevel(s string, sep rune) ([]string, error) {
	var parts []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced parentheses")
			}
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced parentheses")
	}
	if start <= len(s) {
		parts = append(parts, s[start:])
	}
	return parts, nil
}

package http

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/instancez/internal/domain"
)

// WhereNode is a node in the filter expression tree. A node is either a leaf
// (wrapping a single Filter) or an internal AND/OR node with children.
// The root node produced by parseWhere is always an AND over the top-level
// query parameters — an implicit conjunction.
type WhereNode struct {
	Op       string       // "and", "or", or "" for leaf
	Not      bool         // whether the node is negated
	Leaf     *Filter      // set when this is a leaf node
	Children []*WhereNode // set for internal nodes
}

// IsLeaf reports whether n is a leaf node.
func (n *WhereNode) IsLeaf() bool { return n != nil && n.Leaf != nil }

// andLeaves builds a flat AND node from a sequence of leaf filters.
// Convenience for tests and code that doesn't need nesting.
func andLeaves(filters ...Filter) *WhereNode {
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

// buildSQL emits a SQL boolean expression (with $N placeholders) starting
// at argIdx. Returns the expression string, its args, and the next free
// placeholder index. An empty tree returns "".
func (n *WhereNode) buildSQL(argIdx int) (string, []any, int) {
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
		s, a, next := c.buildSQL(argIdx)
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

// colValidator is called on every column reference in a filter tree.
// Implementations return nil to accept the column and an error to reject it.
// The typed path (parseWhere on a known table) passes a closure that checks
// the table's Fields map; RPC untyped setof results pass a permissive validator
// and let Postgres reject unknown columns at execute time.
type colValidator func(col string) error

// parseWhere builds a filter tree from the request query string. Top-level
// parameters are ANDed together. Supported forms:
//
//	col=op.val                     — simple leaf
//	col=not.op.val                 — negated leaf
//	or=(col.op.val,col.op.val)     — disjunction
//	and=(col.op.val,col.op.val)    — explicit conjunction
//	or=(col.op.val,and(col.op.v))  — nested logic lists
//
// Logic lists may contain nested and(...)/or(...) groups and leaves.
func parseWhere(c *gin.Context, tableName string, table domain.Table) (*WhereNode, error) {
	return parseWhereSkip(c, tableName, table, nil)
}

// parseWhereSkip is parseWhere but skips query keys that start with any of
// the given prefixes followed by a dot. Used to route "<embed>.*" query
// parameters into nested embed scopes instead of the outer WHERE.
func parseWhereSkip(c *gin.Context, tableName string, table domain.Table, skipPrefixes map[string]bool) (*WhereNode, error) {
	validate := func(col string) error { return validateColumn(table, col) }
	tree, err := parseWhereWith(c, validate, skipPrefixes, nil)
	if err != nil {
		return nil, err
	}
	if err := validateFTSLeaves(tree, table); err != nil {
		return nil, err
	}
	return tree, nil
}

// ftsOps are the full-text-search operators that require a text-like or
// tsvector target column. Any other operator is unconstrained here.
var ftsOps = map[string]bool{
	"fts":   true,
	"plfts": true,
	"phfts": true,
	"wfts":  true,
}

// isFTSCompatibleType reports whether a field's declared type can be the
// left-hand operand of an FTS operator (fts/plfts/phfts/wfts). Postgres
// accepts text, varchar/character/char, citext, and tsvector directly; all
// produce (or already are) a tsvector when cast. Arrays and non-text types
// are rejected.
func isFTSCompatibleType(t string) bool {
	s := strings.ToLower(strings.TrimSpace(t))
	// Strip type modifiers like "varchar(255)".
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

// validateFTSLeaves walks a parsed WHERE tree and rejects any leaf that
// applies an FTS operator to a column whose declared type isn't text-like
// or tsvector. JSONB `->>` access is allowed (it yields text); JSONB `->`
// access yields jsonb and is rejected. Errors flow up through parseWhere
// and are surfaced as PGRST100 bad_request by the handler.
func validateFTSLeaves(n *WhereNode, table domain.Table) error {
	if n == nil {
		return nil
	}
	if n.Leaf != nil {
		if !ftsOps[n.Leaf.Operator] {
			return nil
		}
		base, steps := splitJSONBPath(n.Leaf.Column)
		// The final step determines the chain's result type:
		// `->>` yields text — allowed. `->` yields jsonb — rejected.
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

// parseWhereWith is the lower-level filter parser. It accepts a colValidator
// callback instead of a concrete domain.Table so RPC call sites — which may
// have no corresponding table at all — can supply their own validation
// strategy (strict against a known table, or permissive for SETOF custom types).
// skipKeys is an exact-match skip set, used by RPC to keep function-argument
// query keys out of the WHERE tree.
func parseWhereWith(c *gin.Context, validate colValidator, skipPrefixes map[string]bool, skipKeys map[string]bool) (*WhereNode, error) {
	root := &WhereNode{Op: "and"}

	for key, values := range c.Request.URL.Query() {
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

// parseLeafValue parses a single filter value for a column param.
// Supports the "not." prefix: e.g. "not.eq.5" → NOT (col = 5).
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

// parseLogicList parses a value like "(a.eq.1,b.eq.2,and(c.eq.3,d.eq.4))"
// associated with top-level param "or" or "and".
func parseLogicList(op, raw string, table domain.Table) (*WhereNode, error) {
	return parseLogicListWith(op, raw, func(col string) error { return validateColumn(table, col) })
}

func parseLogicListWith(op, raw string, validate colValidator) (*WhereNode, error) {
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

// parseLogicItem parses one element of a logic list. It may be:
//   - a nested "and(...)" or "or(...)" group
//   - an optional "not." prefix applied to a nested group
//   - a leaf "col.op.val" or "col.not.op.val"
func parseLogicItem(item string, table domain.Table) (*WhereNode, error) {
	return parseLogicItemWith(item, func(col string) error { return validateColumn(table, col) })
}

func parseLogicItemWith(item string, validate colValidator) (*WhereNode, error) {
	not := false
	if strings.HasPrefix(item, "not.") {
		// Could be "not.and(...)", "not.or(...)", or "not.col.op.val" — the
		// latter is ambiguous; we reserve "not." only for nested groups
		// here and use "col.not.op.val" for negated leaves.
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

	// Leaf: col.[not.]op.val
	// Column may itself contain JSONB operators like "metadata->>theme",
	// which do not contain dots, so splitting on the first dot is safe.
	return parseLogicLeafWith(item, validate)
}

// parseLogicLeaf parses "col.op.val" or "col.not.op.val" into a leaf node.
func parseLogicLeaf(item string, table domain.Table) (*WhereNode, error) {
	return parseLogicLeafWith(item, func(col string) error { return validateColumn(table, col) })
}

func parseLogicLeafWith(item string, validate colValidator) (*WhereNode, error) {
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

// parseHavingParam parses a having= query parameter into a WhereNode tree.
// The having parameter uses the same col.op.value syntax as filter leaves,
// separated by commas for multiple conditions (implicit AND):
//
//	having=count.gt.5               — single condition
//	having=count.gt.5,sum.gte.100   — AND of two conditions
//
// Column names are validated against select-list aggregate aliases (like
// "count" from id.count()) and also against the table's real columns (for
// grouped columns). This mirrors PostgREST's behavior where HAVING
// conditions can reference output-list aliases.
func parseHavingParam(raw string, tableName string, table domain.Table, selectItems []string) (*WhereNode, error) {
	// Build a map of aggregate alias → SQL expression so we can expand
	// aliases in the HAVING clause (Postgres does not resolve SELECT aliases
	// in HAVING).
	aggAliasExprs := map[string]string{}
	for _, s := range selectItems {
		if !isAggSelectEntry(s) {
			continue
		}
		item := parseSelectItem(s)
		alias := item.Alias
		if alias == "" && item.Agg != "" {
			alias = item.Agg
		}
		if alias == "" {
			continue
		}
		// Build the raw SQL expression (without " AS alias").
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

	// Validator: accept aggregate aliases and real table columns (grouped columns).
	validate := func(col string) error {
		if _, ok := aggAliasExprs[col]; ok {
			return nil
		}
		if err := validateColumn(table, col); err == nil {
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
		// Expand aggregate alias to the actual SQL expression.
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

// splitTopLevel splits s on sep at paren depth 0. Tracks '(' and ')'.
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

package postgrest

import (
	"fmt"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// SelectItem is a parsed non-embed entry from the select query param.
// Examples:
//
//	"title"           → {Col: "title"}
//	"nick:name"       → {Alias: "nick", Col: "name"}
//	"age::text"       → {Col: "age", Cast: "text"}
//	"label:age::text" → {Alias: "label", Col: "age", Cast: "text"}
type SelectItem struct {
	Alias string
	Col   string // may include a JSONB path, e.g. "metadata->>theme"
	Cast  string
	Agg   string // one of: count, sum, avg, min, max; empty otherwise
}

// ParseSelectItem splits a select entry into alias, column, cast, and
// aggregate parts. It does not validate the parts; use ValidateSelectItem.
func ParseSelectItem(s string) SelectItem {
	item := SelectItem{}
	// Detect alias via the first ":" that is NOT part of "::".
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ':' {
				break // "::" is cast, not alias
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
	// Aggregate suffix: "col.agg()" or bare "agg()".
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

// ValidateSelectItem checks that the parsed column exists on the table and
// that the alias and cast (if any) are safe identifiers.
func ValidateSelectItem(table domain.Table, item SelectItem) error {
	if item.Alias != "" && !identRe.MatchString(item.Alias) {
		return fmt.Errorf("invalid alias %q", item.Alias)
	}
	if item.Cast != "" && !identRe.MatchString(item.Cast) {
		return fmt.Errorf("invalid cast %q", item.Cast)
	}
	if item.Col == "*" {
		if item.Alias != "" || item.Cast != "" || item.Agg != "" {
			return fmt.Errorf("cannot alias, cast, or aggregate *")
		}
		return nil
	}
	// Bare aggregate (count()) has no column to validate.
	if item.Agg != "" && item.Col == "" {
		if item.Agg != "count" {
			return fmt.Errorf("aggregate %q requires a column", item.Agg)
		}
		return nil
	}
	return ValidateColumn(table, item.Col)
}

// RenderSelectItem emits the SQL expression for a select entry, qualified
// with the given table name. JSONB path expressions are reassembled with a
// single-quoted key (key validity is already checked by ValidateColumn).
func RenderSelectItem(tableName string, item SelectItem) string {
	if item.Col == "*" {
		return tableName + ".*"
	}

	var expr string
	if item.Agg != "" && item.Col == "" {
		// Bare count() → COUNT(*).
		expr = "COUNT(*)"
	} else {
		base, steps := splitJSONBPath(item.Col)
		expr = fmt.Sprintf("%s.%s", tableName, base)
		if len(steps) > 0 {
			expr += renderJSONBSuffix(steps)
		}
		if item.Agg != "" {
			expr = fmt.Sprintf("%s(%s)", strings.ToUpper(item.Agg), expr)
		}
	}
	if item.Cast != "" {
		expr = fmt.Sprintf("(%s)::%s", expr, item.Cast)
	}
	alias := item.Alias
	if alias == "" && item.Agg != "" {
		// PostgREST default: aggregate key is the aggregate name.
		alias = item.Agg
	}
	if alias != "" {
		expr += " AS " + alias
	}
	return expr
}

// RenderSelectItemGroupByExpr returns the SQL expression a non-aggregate
// select item contributes to GROUP BY. It re-renders the base expression
// (qualified, JSONB-expanded, cast-wrapped) without the " AS alias" suffix.
func RenderSelectItemGroupByExpr(tableName string, item SelectItem) string {
	if item.Col == "*" || item.Agg != "" {
		return ""
	}
	base, steps := splitJSONBPath(item.Col)
	expr := fmt.Sprintf("%s.%s", tableName, base)
	if len(steps) > 0 {
		expr += renderJSONBSuffix(steps)
	}
	if item.Cast != "" {
		expr = fmt.Sprintf("(%s)::%s", expr, item.Cast)
	}
	return expr
}

// IsAggSelectEntry reports whether a raw select entry (after splitting on
// top-level commas) is an aggregate like "count()" or "id.count()" rather
// than an embed like "author(*)". Both shapes contain parentheses, so
// callers that distinguish aggregates from embeds must use this helper.
func IsAggSelectEntry(raw string) bool {
	s := raw
	// Strip alias prefix so "total:id.count()" still matches.
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ':' {
				break
			}
			s = s[i+1:]
			break
		}
	}
	// Strip cast suffix so "id.count()::int" still matches.
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

// SplitEmbedAlias splits an embed name like "alias:relation" into
// ("alias", "relation"). Returns ("", s) when no alias prefix is present.
// A "::" sequence is treated as part of a cast and is not an alias separator,
// matching the behaviour of ParseSelectItem on plain select entries.
func SplitEmbedAlias(s string) (alias, name string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ':' {
				break
			}
			return s[:i], s[i+1:]
		}
	}
	return "", s
}

// ParseEmbedHint extracts "!inner"/"!left" join modifiers and an optional
// FK disambiguation hint from an embed name. PostgREST allows syntax like:
//
//	author!inner           — inner join modifier
//	author!fk_name         — FK disambiguation (constraint or column)
//	tasks!assignee!inner   — both, in any order
//
// Returns the clean embed name, the inner flag, and the FK hint (empty if
// no disambiguation was provided). The FK hint is later matched against FK
// column names in resolveEmbeds.
func ParseEmbedHint(raw string) (name string, inner bool, fkHint string) {
	parts := strings.Split(raw, "!")
	name = parts[0]
	for _, p := range parts[1:] {
		switch p {
		case "inner":
			inner = true
		case "left":
			// left is the default join kind; accept and ignore.
		default:
			fkHint = p
		}
	}
	return
}

// CollectSelectAliases returns the set of output-list names produced by
// a parsed select list: explicit aliases (`nick:name`) and the default
// alias key for aggregates without an explicit alias (`id.count()` →
// "count"). Embed entries are skipped.
func CollectSelectAliases(selectItems []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range selectItems {
		if strings.Contains(s, "(") && !IsAggSelectEntry(s) {
			continue
		}
		item := ParseSelectItem(s)
		switch {
		case item.Alias != "":
			out[item.Alias] = struct{}{}
		case item.Agg != "":
			out[item.Agg] = struct{}{}
		}
	}
	return out
}

// ParseOrderValueWithSelect is ParseOrderValueWith knowledge of the
// select list, so order tokens that match a select alias (explicit or
// the default key of an aggregate) bypass column validation and are
// emitted as quoted output-list references in the generated SQL. This
// mirrors PostgREST, where `order=count.desc` against a select of
// `...,id.count()` sorts by the aggregate output rather than failing
// with "unknown column".
func ParseOrderValueWithSelect(val string, table domain.Table, selectItems []string) ([]OrderClause, error) {
	aliases := CollectSelectAliases(selectItems)
	validate := func(col string) error {
		if _, ok := aliases[col]; ok {
			return nil
		}
		return ValidateColumn(table, col)
	}
	clauses, err := ParseOrderValueWith(val, validate)
	if err != nil {
		return nil, err
	}
	for i := range clauses {
		if _, ok := aliases[clauses[i].Column]; ok {
			clauses[i].IsAlias = true
		}
	}
	return clauses, nil
}

// ParseOrderValueWith parses a comma-separated PostgREST order list into
// OrderClauses using a pluggable column validator.
// Each entry is col[.asc|.desc][.nullsfirst|.nullslast];
// modifiers are stripped right-to-left so the remaining prefix is the
// column name (default ASC, server default nulls order).
func ParseOrderValueWith(val string, validate ColValidator) ([]OrderClause, error) {
	var out []OrderClause
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		oc := OrderClause{}
		for {
			switch {
			case strings.HasSuffix(part, ".nullsfirst"):
				oc.Nulls = "first"
				part = strings.TrimSuffix(part, ".nullsfirst")
				continue
			case strings.HasSuffix(part, ".nullslast"):
				oc.Nulls = "last"
				part = strings.TrimSuffix(part, ".nullslast")
				continue
			case strings.HasSuffix(part, ".desc"):
				oc.Desc = true
				part = strings.TrimSuffix(part, ".desc")
				continue
			case strings.HasSuffix(part, ".asc"):
				part = strings.TrimSuffix(part, ".asc")
				continue
			}
			break
		}
		oc.Column = part
		if err := validate(oc.Column); err != nil {
			return nil, err
		}
		out = append(out, oc)
	}
	return out, nil
}

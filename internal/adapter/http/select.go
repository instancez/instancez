package http

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// identRe matches a safe SQL identifier (alias or cast type). Casts are
// interpolated directly into SQL, so we refuse anything outside alnum/_.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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

// aggNames is the set of PostgREST-style aggregate suffixes recognised in a
// select entry. Order here is irrelevant — callers match by exact token.
var aggNames = []string{"count", "sum", "avg", "min", "max"}

// parseSelectItem splits a select entry into alias, column, cast, and
// aggregate parts. It does not validate the parts; use validateSelectItem.
func parseSelectItem(s string) SelectItem {
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

// validateSelectItem checks that the parsed column exists on the table and
// that the alias and cast (if any) are safe identifiers.
func validateSelectItem(table domain.Table, item SelectItem) error {
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
	return validateColumn(table, item.Col)
}

// renderSelectItem emits the SQL expression for a select entry, qualified
// with the given table name. JSONB path expressions are reassembled with a
// single-quoted key (key validity is already checked by validateColumn).
func renderSelectItem(tableName string, item SelectItem) string {
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

// renderSelectItemGroupByExpr returns the SQL expression a non-aggregate
// select item contributes to GROUP BY. It re-renders the base expression
// (qualified, JSONB-expanded, cast-wrapped) without the " AS alias" suffix.
func renderSelectItemGroupByExpr(tableName string, item SelectItem) string {
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

// isAggSelectEntry reports whether a raw select entry (after splitting on
// top-level commas) is an aggregate like "count()" or "id.count()" rather
// than an embed like "author(*)". Both shapes contain parentheses, so
// callers that distinguish aggregates from embeds must use this helper.
func isAggSelectEntry(raw string) bool {
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

// splitEmbedAlias splits an embed name like "alias:relation" into
// ("alias", "relation"). Returns ("", s) when no alias prefix is present.
// A "::" sequence is treated as part of a cast and is not an alias separator,
// matching the behaviour of parseSelectItem on plain select entries.
func splitEmbedAlias(s string) (alias, name string) {
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

// parseEmbedHint extracts "!inner"/"!left" join modifiers and an optional
// FK disambiguation hint from an embed name. PostgREST allows syntax like:
//
//	author!inner           — inner join modifier
//	author!fk_name         — FK disambiguation (constraint or column)
//	tasks!assignee!inner   — both, in any order
//
// Returns the clean embed name, the inner flag, and the FK hint (empty if
// no disambiguation was provided). The FK hint is later matched against FK
// column names in resolveEmbeds.
func parseEmbedHint(raw string) (name string, inner bool, fkHint string) {
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

package http

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/saedx1/ultrabase/internal/domain"
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
}

// parseSelectItem splits a select entry into alias, column, and cast parts.
// It does not validate the parts; use validateSelectItem for that.
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
		item.Col = s[:idx]
		item.Cast = s[idx+2:]
	} else {
		item.Col = s
	}
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
		if item.Alias != "" || item.Cast != "" {
			return fmt.Errorf("cannot alias or cast *")
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

	base, op, key := splitJSONBColumn(item.Col)
	expr := fmt.Sprintf("%s.%s", tableName, base)
	if op != "" {
		expr = fmt.Sprintf("%s.%s%s'%s'", tableName, base, op, key)
	}
	if item.Cast != "" {
		expr = fmt.Sprintf("(%s)::%s", expr, item.Cast)
	}
	if item.Alias != "" {
		expr += " AS " + item.Alias
	}
	return expr
}

// parseEmbedHint extracts an optional "!inner" or "!left" hint from an embed
// name prefix like "author!inner". Returns the clean name and the inner flag.
func parseEmbedHint(raw string) (string, bool) {
	if idx := strings.Index(raw, "!"); idx != -1 {
		name := raw[:idx]
		hint := raw[idx+1:]
		return name, hint == "inner"
	}
	return raw, false
}

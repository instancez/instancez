package cli

import (
	"fmt"
	"strings"

	"github.com/instancez/instancez/internal/cloud"
)

// changeSymbol maps a ChangeKind to its rendered prefix.
func changeSymbol(k cloud.ChangeKind) string {
	switch k {
	case cloud.ChangeAdded:
		return "+"
	case cloud.ChangeRemoved:
		return "-"
	default:
		return "~"
	}
}

// renderConfigDiff formats a page-free ConfigDiff as plain text for terminal
// output. Used by both `inz cloud deploy` and `inz validate --project` so
// their diff output is identical.
func renderConfigDiff(diff cloud.ConfigDiff) string {
	if !diff.HasChanges {
		return "No pending changes."
	}
	var b strings.Builder
	for _, t := range diff.Tables {
		fmt.Fprintf(&b, "  %s table %s\n", changeSymbol(t.Change), t.Name)
		for _, c := range t.Columns {
			fmt.Fprintf(&b, "    %s column %s\n", changeSymbol(c.Change), c.Name)
		}
	}
	for _, s := range diff.Sections {
		fmt.Fprintf(&b, "  %s config %s\n", changeSymbol(s.Change), s.Path)
	}
	return strings.TrimRight(b.String(), "\n")
}

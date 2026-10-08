package vet

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

var (
	bodyAuthRef = regexp.MustCompile(`(?i)auth\s*\.\s*(uid|jwt|role)\s*\(`)
	dynamicSQL  = regexp.MustCompile(`(?is)\bexecute\b[^;]*(\|\||\bformat\s*\([^;]*%(\d+\$)?s)`)
)

func isDefiner(fn domain.Function) bool { return strings.EqualFold(fn.Security, "definer") }

func ruleRPC(c *ctx) {
	for _, name := range slices.Sorted(maps.Keys(c.cfg.RPC)) {
		fn := c.cfg.RPC[name]
		if !isDefiner(fn) {
			if dynamicSQL.MatchString(fn.Body) && strings.EqualFold(fn.Language, "plpgsql") {
				dynamicSQLFinding(c, name, Medium)
			}
			continue
		}
		if !fn.AuthRequired {
			sev := High
			if bodyAuthRef.MatchString(fn.Body) {
				sev = Medium
			}
			c.add("rpc-definer-no-auth", sev, []any{"rpc", name, "auth_required"},
				"Public function runs with owner rights",
				fmt.Sprintf("%s is security definer and callable without signing in, so anonymous callers get its owner's access and skip RLS. Found by a raw text scan of the body.", name),
				"Set auth_required: true, or check auth.uid() inside the body.")
		}
		if _, pinned := fn.Set["search_path"]; !pinned {
			c.add("rpc-definer-search-path", High, []any{"rpc", name, "set", "search_path"},
				"Definer function has no pinned search_path",
				fmt.Sprintf("%s is security definer without a pinned search_path, so callers can shadow the objects its body uses.", name),
				`Add set: { search_path: "" } and schema-qualify names in the body.`)
		}
		if strings.EqualFold(fn.Language, "plpgsql") && dynamicSQL.MatchString(fn.Body) {
			dynamicSQLFinding(c, name, High)
		}
	}
}

func dynamicSQLFinding(c *ctx, name string, sev Severity) {
	c.add("rpc-dynamic-sql", sev, []any{"rpc", name, "body"},
		"Function builds SQL from text",
		fmt.Sprintf("%s runs EXECUTE on a string built with || or format(%%s), which can allow SQL injection if any part comes from the caller.", name),
		"Use format with %I and %L, or pass values with EXECUTE ... USING.")
}

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
	bodyAuthRef = regexp.MustCompile(`(?i)auth\s*\.\s*(uid|jwt|email|role)\s*\(`)
	bodyWrites  = regexp.MustCompile(`(?i)\b(insert|update|delete|truncate|drop|alter|create|grant|execute)\b`)
	dynamicSQL  = regexp.MustCompile(`(?is)\bexecute\b[^;]*(\|\||\bformat\s*\([^;]*%(\d+\$)?s)`)
)

func isDefiner(fn domain.Function) bool { return strings.EqualFold(fn.Security, "definer") }

func ruleRPC(c *ctx) {
	for _, name := range slices.Sorted(maps.Keys(c.cfg.RPC)) {
		fn := c.cfg.RPC[name]
		definer := isDefiner(fn)
		if definer {
			if !fn.AuthRequired {
				writes, ident := bodyWrites.MatchString(fn.Body), bodyAuthRef.MatchString(fn.Body)
				sev := map[[2]bool]Severity{{true, false}: High, {true, true}: Medium, {false, false}: Medium, {false, true}: Low}[[2]bool{writes, ident}]
				c.add("rpc-definer-no-auth", sev, []any{"rpc", name, "auth_required"},
					"Public function runs with owner rights",
					fmt.Sprintf("%s is security definer and callable without signing in, so anonymous callers run it with its owner's access: whatever it reads bypasses RLS, and whatever it writes is unchecked. Found by a raw text scan of the body.", name),
					"Keep it public only if the data it returns is meant to be public; otherwise set auth_required: true.")
			}
			if _, pinned := fn.Set["search_path"]; !pinned {
				c.add("rpc-definer-search-path", High, []any{"rpc", name, "set", "search_path"},
					"Definer function has no pinned search_path",
					fmt.Sprintf("%s is security definer without a pinned search_path, so callers can shadow the objects its body uses.", name),
					`Add set: { search_path: "" } and schema-qualify names in the body.`)
			}
		}
		if strings.EqualFold(fn.Language, "plpgsql") && dynamicSQL.MatchString(fn.Body) {
			sev := Medium
			if definer {
				sev = High
			}
			dynamicSQLFinding(c, name, sev)
		}
	}
}

func dynamicSQLFinding(c *ctx, name string, sev Severity) {
	c.add("rpc-dynamic-sql", sev, []any{"rpc", name, "body"},
		"Function builds SQL from text",
		fmt.Sprintf("%s runs EXECUTE on a string built with || or format(%%s), which can allow SQL injection if any part comes from the caller.", name),
		"Use format with %I and %L, or pass values with EXECUTE ... USING.")
}

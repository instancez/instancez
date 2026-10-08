package vet

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
)

var secretEnvName = regexp.MustCompile(`(?i)password|secret|token|key|credential|private|pass`)

func ruleFunctionSecrets(c *ctx) {
	for _, name := range slices.Sorted(maps.Keys(c.cfg.Functions)) {
		fn := c.cfg.Functions[name]
		if fn.AuthRequired {
			continue
		}
		for _, k := range slices.Sorted(maps.Keys(fn.Env)) {
			if secretEnvName.MatchString(k) {
				c.add("function-public-secrets", Medium, []any{"functions", name, "auth_required"},
					"Public function can use secrets",
					fmt.Sprintf("Function %s can be called without signing in and has secret-looking env %s in scope.", name, k),
					"Set auth_required: true, or make sure the function never exposes what that value unlocks.")
				break
			}
		}
	}
}

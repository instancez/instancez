package vet

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

func ruleBuckets(c *ctx) {
	anyRLS := slices.ContainsFunc(slices.Collect(maps.Values(c.cfg.Storage)), func(b domain.Bucket) bool { return len(b.RLS) > 0 })
	for _, name := range slices.Sorted(maps.Keys(c.cfg.Storage)) {
		b := c.cfg.Storage[name]
		at := []any{"storage", name}
		for i, p := range b.RLS {
			if !strings.EqualFold(p.Type, "restrictive") {
				openWrite(c, "bucket-open-write", p, append(slices.Clone(at), "rls", i), "bucket "+name)
			}
		}
		if len(b.RLS) == 0 {
			sev := Medium
			if anyRLS {
				sev = High
			}
			c.add("bucket-no-rls", sev, at,
				"Bucket has no per-user access rules",
				fmt.Sprintf("Bucket %s has no rls policies, so any JWT holder, including anonymous sign-in users, can write and delete its objects.", name),
				"Add rls policies scoped to the caller, for example based on the object path and auth.uid().")
		}
		if b.Public {
			c.add("bucket-public", Low, append(slices.Clone(at), "public"),
				"Bucket is public",
				fmt.Sprintf("Anyone with a URL can read objects in %s. Public skips RLS for reads only; writes still follow the policies.", name),
				"Keep it only for content that is meant to be public.")
		}
	}
}

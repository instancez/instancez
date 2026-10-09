package vet

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

const (
	anyCaller = "any caller, signed in or not,"
	anyJWT    = "any caller with a JWT (including anonymous sign-in users)"
)

var writeOps = []string{"insert", "update", "delete"}

func tableLabel(name string, t domain.Table) string {
	if s := t.EffectiveSchema(); s != "public" {
		return s + "." + name
	}
	return name
}

func ruleRLSDisabled(c *ctx) {
	for _, name := range slices.Sorted(maps.Keys(c.cfg.Tables)) {
		t := c.cfg.Tables[name]
		if t.EffectiveRLSEnabled() {
			continue
		}
		c.add("rls-disabled", Critical, []any{"tables", name, "rls_enabled"},
			"Row-level security is off",
			fmt.Sprintf("Anyone with the public key can read and write every row in %s.", tableLabel(name, t)),
			"Set rls_enabled: true and add policies, unless the table is meant to be public.")
		c.withEdit(Edit{Path: []any{"tables", name, "rls_enabled"}, Value: true})
	}
}

func hasSensitiveColumn(t domain.Table) bool {
	return slices.ContainsFunc(t.Fields, func(f domain.Field) bool { return hasSensitiveSegment(f.Name) })
}

var ownerColumns = []string{"user_id", "owner_id", "created_by", "author_id"}

func hasOwnerColumn(t domain.Table) bool {
	return slices.ContainsFunc(t.Fields, func(f domain.Field) bool {
		if slices.Contains(ownerColumns, strings.ToLower(f.Name)) {
			return true
		}
		if f.ForeignKey == nil {
			return false
		}
		s, tbl, col, err := domain.ParseFKReference(f.ForeignKey.References)
		return err == nil && s == "auth" && tbl == "users" && col == "id"
	})
}

// opsOf returns the policy operations that are in set.
func opsOf(p domain.RLSPolicy, set ...string) []string {
	var out []string
	for _, op := range p.Operations {
		if slices.Contains(set, strings.ToLower(op)) {
			out = append(out, strings.ToLower(op))
		}
	}
	return out
}

func rulePolicies(c *ctx) {
	for _, name := range slices.Sorted(maps.Keys(c.cfg.Tables)) {
		t := c.cfg.Tables[name]
		if !t.EffectiveRLSEnabled() {
			continue
		}
		label := tableLabel(name, t)
		for i, p := range t.RLS {
			if strings.EqualFold(p.Type, "restrictive") {
				continue
			}
			at := []any{"tables", name, "rls", i}
			openWrite(c, "policy-open-write", p, at, anyCaller, label)
			openRead(c, p, at, label, t)
			noIdentityWrite(c, p, at, label, t)
		}
	}
}

func openWrite(c *ctx, rule string, p domain.RLSPolicy, at []any, who, label string) {
	for _, op := range opsOf(p, writeOps...) {
		usesUsing, usesCheck := op != "insert", op != "delete"
		for _, f := range []struct {
			name, expr string
			used       bool
		}{{"using", p.Using, usesUsing}, {"with_check", p.WithCheck, usesCheck}} {
			if f.used && isLiteralTrue(f.expr) {
				c.add(rule, Critical, append(slices.Clone(at), f.name),
					"Anyone can change rows",
					fmt.Sprintf("This policy lets %s %s rows in %s.", who, op, label),
					"Scope it to the caller, for example auth.uid() = user_id.")
				return
			}
		}
	}
}

func openRead(c *ctx, p domain.RLSPolicy, at []any, label string, t domain.Table) {
	if len(opsOf(p, "select")) == 0 {
		return
	}
	at = append(slices.Clone(at), "using")
	switch {
	case isLiteralTrue(p.Using):
		sev, extra := Low, ""
		if hasSensitiveColumn(t) {
			sev, extra = High, " The table has sensitive columns, and the API returns every column."
		}
		c.add("policy-open-read", sev, at,
			"Anyone can read every row",
			fmt.Sprintf("This policy lets any caller, signed in or not, read all rows in %s.%s", label, extra),
			"Scope it to the caller, or keep it only if the table is truly public.")
	case isAuthGateOnly(p.Using) && hasSensitiveColumn(t):
		c.add("policy-authed-read-sensitive", Medium, at,
			"Every signed-in user can read every row",
			fmt.Sprintf("This policy only checks that someone is signed in, and %s has sensitive columns. Anonymous sign-ins count too.", label),
			"Scope it to the caller, for example auth.uid() = user_id.")
	}
}

func noIdentityWrite(c *ctx, p domain.RLSPolicy, at []any, label string, t domain.Table) {
	var exprs []string
	for _, op := range opsOf(p, writeOps...) {
		exprs = append(exprs, exprsFor(op, p)...)
	}
	if len(exprs) == 0 || slices.ContainsFunc(exprs, isLiteralTrue) || !slices.ContainsFunc(exprs, func(e string) bool { return squash(e) != "false" }) {
		return
	}
	for _, e := range exprs {
		if refsIdentity(e) || strings.Contains(squash(e), "service_role") || c.callsIdentityRPC(e) {
			return
		}
	}
	sev := Medium
	if hasOwnerColumn(t) {
		sev = High
	}
	field := "using"
	if strings.TrimSpace(p.Using) == "" {
		field = "with_check"
	}
	c.add("policy-no-identity-write", sev, append(slices.Clone(at), field),
		"Write policy ignores who the caller is",
		fmt.Sprintf("This policy never checks the caller's identity, so any user who passes it can change other users' rows in %s.", label),
		"Add an identity check, for example auth.uid() = user_id.")
}

func ruleBuckets(c *ctx) {
	anyRLS := slices.ContainsFunc(slices.Collect(maps.Values(c.cfg.Storage)), func(b domain.Bucket) bool { return len(b.RLS) > 0 })
	for _, name := range slices.Sorted(maps.Keys(c.cfg.Storage)) {
		b := c.cfg.Storage[name]
		at := []any{"storage", name}
		for i, p := range b.RLS {
			if !strings.EqualFold(p.Type, "restrictive") {
				pat := append(slices.Clone(at), "rls", i)
				openWrite(c, "bucket-open-write", p, pat, anyJWT, "bucket "+name)
				if !b.Public && len(opsOf(p, "select")) > 0 && isLiteralTrue(p.Using) {
					c.add("bucket-open-read", Medium, append(pat, "using"), "Anyone can read every object",
						fmt.Sprintf("This policy lets %s list and read all objects in bucket %s.", anyJWT, name),
						"Scope it to the caller, for example by object path and auth.uid(), or make the bucket public if that is intended.")
				}
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
			c.withEdit(Edit{Path: append(slices.Clone(at), "public"), Value: false})
		}
	}
}

// callsIdentityRPC reports whether expr calls a user-declared rpc whose body reads the caller's identity.
func (c *ctx) callsIdentityRPC(expr string) bool {
	for name, fn := range c.cfg.RPC {
		if !bodyAuthRef.MatchString(fn.Body) {
			continue
		}
		call := regexp.MustCompile(`(?i)(?:^|[^\w.])(?:public\s*\.\s*)?` + regexp.QuoteMeta(name) + `\s*\(`)
		if call.MatchString(expr) {
			return true
		}
	}
	return false
}

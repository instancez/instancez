package vet

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
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

func isLiteralFalse(expr string) bool { return squash(expr) == "false" }

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
			openWrite(c, p, at, label)
			openRead(c, p, at, label, t)
			noIdentityWrite(c, p, at, label, t)
		}
	}
}

func openWrite(c *ctx, p domain.RLSPolicy, at []any, label string) {
	for _, op := range opsOf(p, writeOps...) {
		usesUsing, usesCheck := op != "insert", op != "delete"
		for _, f := range []struct {
			name, expr string
			used       bool
		}{{"using", p.Using, usesUsing}, {"with_check", p.WithCheck, usesCheck}} {
			if f.used && isLiteralTrue(f.expr) {
				c.add("policy-open-write", Critical, append(slices.Clone(at), f.name),
					"Anyone can change rows",
					fmt.Sprintf("This policy lets any caller, signed in or not, %s rows in %s.", op, label),
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
	if len(exprs) == 0 || slices.ContainsFunc(exprs, isLiteralTrue) || !slices.ContainsFunc(exprs, func(e string) bool { return !isLiteralFalse(e) }) {
		return
	}
	for _, e := range exprs {
		if refsIdentity(e) || strings.Contains(squash(e), "service_role") {
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

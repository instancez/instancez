package vet

import (
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

func squash(expr string) string {
	return strings.ToLower(strings.Join(strings.Fields(expr), ""))
}

// isLiteralTrue reports whether expr is a constant-true predicate such as `( TRUE )` or `1=1`.
func isLiteralTrue(expr string) bool {
	s := squash(expr)
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = s[1 : len(s)-1]
	}
	return s == "true" || s == "1=1"
}

// refsIdentity reports whether expr ties access to the caller's identity.
func refsIdentity(expr string) bool {
	s := squash(expr)
	return strings.Contains(s, "auth.uid(") || strings.Contains(s, "auth.jwt(") || strings.Contains(s, "auth.email(")
}

// isAuthGateOnly reports whether expr only checks that someone is signed in, with no identity scoping.
func isAuthGateOnly(expr string) bool {
	s := strings.ReplaceAll(squash(expr), "auth.uid()isnotnull", "auth.is_authenticated()")
	if refsIdentity(s) {
		return false
	}
	return strings.Contains(s, "auth.is_authenticated(") || strings.Contains(s, "auth.role(")
}

// exprsFor returns the expressions that gate op under policy p.
func exprsFor(op string, p domain.RLSPolicy) []string {
	var exprs []string
	switch op {
	case "select", "delete":
		exprs = []string{p.Using}
	case "insert":
		exprs = []string{p.WithCheck}
	case "update":
		check := p.WithCheck
		if strings.TrimSpace(check) == "" {
			check = p.Using
		}
		exprs = []string{p.Using, check}
	}
	return slices.DeleteFunc(exprs, func(e string) bool { return strings.TrimSpace(e) == "" })
}

var sensitiveSegments = []string{"password", "passwd", "secret", "token", "apikey", "ssn", "email", "phone", "dob", "iban"}

func wordSegments(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '_' || r == '-' })
}

// hasSensitiveSegment matches whole underscore-separated words, so ip_address is not sensitive but api_key is.
func hasSensitiveSegment(name string) bool {
	segs := wordSegments(name)
	for i, s := range segs {
		if slices.Contains(sensitiveSegments, s) || (s == "api" && i+1 < len(segs) && segs[i+1] == "key") {
			return true
		}
	}
	return false
}

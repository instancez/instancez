package vet

import (
	"slices"
	"strings"
	"unicode"

	"github.com/instancez/instancez/internal/domain"
)

func squash(expr string) string {
	return strings.ToLower(strings.Join(strings.Fields(expr), ""))
}

// isLiteralTrue reports whether expr is a constant-true predicate such as `( TRUE )` or `1=1`.
func isLiteralTrue(expr string) bool {
	s := squash(expr)
	for prev := ""; prev != s; {
		prev = s
		s = strings.TrimSuffix(s, "::boolean")
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
			s = s[1 : len(s)-1]
		}
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

var sensitiveWords = []string{"password", "passwd", "secret", "token", "apikey", "api_key", "ssn", "email", "phone", "dob", "iban"}

// wordSegments splits on _, - and camelCase humps.
func wordSegments(name string) []string {
	var b strings.Builder
	prev := ' '
	for _, r := range name {
		if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
		prev = r
	}
	return strings.FieldsFunc(b.String(), func(r rune) bool { return r == '_' || r == '-' })
}

// hasSensitiveSegment matches whole words, so ip_address is not sensitive but api_key and passwordHash are.
func hasSensitiveSegment(name string) bool {
	joined := "_" + strings.Join(wordSegments(name), "_") + "_"
	return slices.ContainsFunc(sensitiveWords, func(w string) bool { return strings.Contains(joined, "_"+w+"_") })
}

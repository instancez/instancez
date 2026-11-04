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

// scanSQL blanks comments in clean and also blanks string and quoted-identifier contents in mask; both keep expr's length.
func scanSQL(expr string) (clean, mask string) {
	c, m := []byte(expr), []byte(expr)
	for i := 0; i < len(c); i++ {
		switch {
		case c[i] == '\'' || c[i] == '"':
			q := c[i]
			for i++; i < len(c); i++ {
				if c[i] == q {
					if i+1 < len(c) && c[i+1] == q {
						m[i], m[i+1] = 'x', 'x'
						i++
						continue
					}
					break
				}
				m[i] = 'x'
			}
		case c[i] == '-' && i+1 < len(c) && c[i+1] == '-':
			for ; i < len(c) && c[i] != '\n'; i++ {
				c[i], m[i] = ' ', ' '
			}
		case c[i] == '/' && i+1 < len(c) && c[i+1] == '*':
			for depth := 0; i < len(c); i++ {
				switch {
				case c[i] == '/' && i+1 < len(c) && c[i+1] == '*':
					depth++
					c[i], m[i] = ' ', ' '
					i++
				case c[i] == '*' && i+1 < len(c) && c[i+1] == '/':
					depth--
					c[i], m[i] = ' ', ' '
					i++
				}
				c[i], m[i] = ' ', ' '
				if depth == 0 {
					break
				}
			}
		}
	}
	return string(c), string(m)
}

type sqlTok struct {
	word       string
	start, end int
	depth      int
}

func isWordByte(b byte) bool {
	return b == '_' || b == '$' || b == '.' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

// sqlWords lists the words of mask with their paren and CASE nesting depth.
func sqlWords(mask string) []sqlTok {
	var toks []sqlTok
	depth := 0
	for i := 0; i < len(mask); {
		switch {
		case mask[i] == '(':
			depth++
			i++
		case mask[i] == ')':
			depth--
			i++
		case isWordByte(mask[i]):
			j := i
			for j < len(mask) && isWordByte(mask[j]) {
				j++
			}
			w := strings.ToLower(mask[i:j])
			switch w {
			case "case":
				toks = append(toks, sqlTok{w, i, j, depth})
				depth++
			case "end":
				depth--
				toks = append(toks, sqlTok{w, i, j, depth})
			default:
				toks = append(toks, sqlTok{w, i, j, depth})
			}
			i = j
		default:
			i++
		}
	}
	return toks
}

// splitTop splits expr on keyword kw outside parentheses, CASE blocks, strings, quoted identifiers and comments.
func splitTop(expr, kw string) []string {
	expr, mask := scanSQL(stripOuterParens(expr))
	start := 0
	var parts []string
	for _, t := range sqlWords(mask) {
		if t.depth == 0 && t.word == kw {
			parts = append(parts, expr[start:t.start])
			start = t.end
		}
	}
	return append(parts, expr[start:])
}

// stripOuterParens removes parentheses that wrap the whole expression.
func stripOuterParens(expr string) string {
	for {
		clean, mask := scanSQL(expr)
		t, m := strings.TrimSpace(clean), strings.TrimSpace(mask)
		if len(t) < 2 || t[0] != '(' || t[len(t)-1] != ')' {
			return strings.TrimSpace(clean)
		}
		depth := 0
		for i := 0; i < len(m); i++ {
			switch m[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 && i < len(m)-1 {
					return strings.TrimSpace(clean)
				}
			}
		}
		expr = t[1 : len(t)-1]
	}
}

// caseArms returns the WHEN conditions, their results and the ELSE result when expr is one searched CASE.
func caseArms(expr string) (conds, results []string, elseRes string, ok bool) {
	clean, mask := scanSQL(stripOuterParens(expr))
	toks := sqlWords(mask)
	last := len(toks) - 1
	if last < 1 || toks[0].word != "case" || toks[0].start != 0 || toks[last].word != "end" || toks[last].end != len(mask) {
		return nil, nil, "", false
	}
	for _, t := range toks[1:last] {
		if t.depth == 0 {
			return nil, nil, "", false
		}
	}
	var marks []sqlTok
	for _, t := range toks[1:last] {
		if t.depth == 1 && (t.word == "when" || t.word == "then" || t.word == "else") {
			marks = append(marks, t)
		}
	}
	marks = append(marks, toks[last])
	for i, m := range marks[:len(marks)-1] {
		text := clean[m.end:marks[i+1].start]
		switch m.word {
		case "when":
			conds = append(conds, text)
		case "then":
			results = append(results, text)
		default:
			elseRes = text
		}
	}
	return conds, results, elseRes, len(conds) == len(results)
}

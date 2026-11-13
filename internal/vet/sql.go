package vet

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/instancez/instancez/internal/domain"
)

func squash(expr string) string {
	return strings.ToLower(strings.Join(strings.Fields(expr), ""))
}

// literalBool returns "true" or "false" when expr is a constant such as `( TRUE )`, `1=1` or `0=1`.
func literalBool(expr string) string {
	s := squash(expr)
	for prev := ""; prev != s; {
		prev = s
		s = strings.TrimSuffix(s, "::boolean")
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
			s = s[1 : len(s)-1]
		}
	}
	switch s {
	case "true", "1=1":
		return "true"
	case "false", "0=1", "1=0":
		return "false"
	}
	return ""
}

// isLiteralTrue reports whether expr is a constant-true predicate such as `( TRUE )` or `1=1`.
func isLiteralTrue(expr string) bool { return literalBool(expr) == "true" }

var (
	signInGate  = regexp.MustCompile(`(^|[(]|and|or|select|return|where)(?:\(select)?auth\.(?:uid|jwt|email)\(\)\)?isnotnull`)
	settingCall = regexp.MustCompile(`(?i)\bcurrent_setting\s*\(\s*'`)
	claimName   = regexp.MustCompile(`^request\.jwt\.(claims|claim\.(sub|email))'`)
)

// refsIdentity reports whether expr ties access to the caller's identity; a bare IS NOT NULL sign-in check does not.
func refsIdentity(expr string) bool {
	clean, mask := scanSQL(expr)
	s := signInGate.ReplaceAllString(squash(mask), "$1")
	if strings.Contains(s, "auth.uid(") || strings.Contains(s, "auth.jwt(") || strings.Contains(s, "auth.email(") {
		return true
	}
	for _, loc := range settingCall.FindAllStringIndex(mask, -1) {
		if claimName.MatchString(strings.ToLower(clean[loc[1]:])) {
			return true
		}
	}
	return false
}

// isAuthGateOnly reports whether expr only checks that someone is signed in, with no identity scoping.
func isAuthGateOnly(expr string) bool {
	if refsIdentity(expr) {
		return false
	}
	_, mask := scanSQL(expr)
	s := squash(mask)
	return strings.Contains(s, "auth.is_authenticated(") || strings.Contains(s, "auth.role(") || signInGate.MatchString(s)
}

// constBool folds expr to a constant through nested AND, OR, NOT, IS and COALESCE; ok is false when it is not constant.
func constBool(expr string, depth int, deep *bool) (v, ok bool) {
	if depth > maxGateDepth {
		*deep = true
		return false, false
	}
	e := stripOuterParens(expr)
	if strings.TrimSpace(e) == "" {
		return false, false
	}
	next := func(x string) (bool, bool) { return constBool(x, depth+1, deep) }
	if parts := splitTop(e, "or"); len(parts) > 1 {
		allFalse := true
		for _, p := range parts {
			pv, pok := next(p)
			if pok && pv {
				return true, true
			}
			allFalse = allFalse && pok
		}
		return false, allFalse
	}
	if parts := splitTop(e, "and"); len(parts) > 1 {
		allTrue := true
		for _, p := range parts {
			pv, pok := next(p)
			if pok && !pv {
				return false, true
			}
			allTrue = allTrue && pok
		}
		return allTrue, allTrue
	}
	switch literalBool(e) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	_, mask := scanSQL(e)
	toks := sqlWords(mask)
	if len(toks) == 0 {
		return false, false
	}
	if toks[0].word == "not" && toks[0].start == 0 {
		if rv, rok := next(e[toks[0].end:]); rok {
			return !rv, true
		}
		return false, false
	}
	if n := len(toks); n >= 3 && toks[n-1].depth == 0 && toks[n-2].depth == 0 {
		last, mid := toks[n-1].word, toks[n-2].word
		neg := mid == "not" && toks[n-3].word == "is"
		if (mid == "is" || neg) && (last == "true" || last == "false") {
			cut := toks[n-2].start
			if neg {
				cut = toks[n-3].start
			}
			if bv, bok := next(e[:cut]); bok {
				return (bv == (last == "true")) != neg, true
			}
			return false, false
		}
	}
	if toks[0].word == "coalesce" && toks[0].start == 0 {
		rest := strings.TrimSpace(e[toks[0].end:])
		inner := stripOuterParens(rest)
		if strings.HasPrefix(rest, "(") && inner != rest {
			first, _, _ := strings.Cut(topLevelCommas(inner), "\x00")
			if fv, fok := next(first); fok && fv {
				return true, true
			}
		}
	}
	return false, false
}

// topLevelCommas replaces commas outside parentheses and literals with NUL.
func topLevelCommas(expr string) string {
	clean, mask := scanSQL(expr)
	b := []byte(clean)
	depth := 0
	for i := range mask {
		switch mask[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				b[i] = 0
			}
		}
	}
	return string(b)
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

// scanSQL blanks comments in clean, and literal contents too in mask.
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
			if w == "end" {
				depth--
			}
			toks = append(toks, sqlTok{w, i, j, depth})
			if w == "case" {
				depth++
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
	clean, mask := scanSQL(expr)
	match := make([]int, len(mask))
	var stack []int
	for i := 0; i < len(mask); i++ {
		switch mask[i] {
		case '(':
			stack = append(stack, i)
		case ')':
			if len(stack) > 0 {
				match[stack[len(stack)-1]] = i
				stack = stack[:len(stack)-1]
			}
		}
	}
	lo, hi := 0, len(mask)-1
	for {
		for lo < hi && strings.ContainsRune(" \t\r\n", rune(mask[lo])) {
			lo++
		}
		for hi > lo && strings.ContainsRune(" \t\r\n", rune(mask[hi])) {
			hi--
		}
		if lo >= hi || mask[lo] != '(' || match[lo] != hi {
			break
		}
		lo, hi = lo+1, hi-1
	}
	if lo > hi {
		return ""
	}
	return strings.TrimSpace(clean[lo : hi+1])
}

// caseArms returns the WHEN conditions, their results and the ELSE result when expr is one searched CASE.
func caseArms(expr string) (conds, results []string, elseRes string, ok bool) {
	clean, mask := scanSQL(stripOuterParens(expr))
	toks := sqlWords(mask)
	last := len(toks) - 1
	if last < 1 || toks[0].word != "case" || toks[0].start != 0 || toks[last].word != "end" || toks[last].end != len(mask) {
		return nil, nil, "", false
	}
	var marks []sqlTok
	for _, t := range toks[1:last] {
		if t.depth == 0 {
			return nil, nil, "", false
		}
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

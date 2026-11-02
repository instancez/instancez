package config

import (
	"fmt"
	"strings"
)

const sqlOpChars = "+-*/<>=~!@#%^&|?"

var sqlComparisons = map[string]bool{"=": true, "<>": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true}

type sqlTok struct {
	kind byte // 'w' word, 'o' operator, 'q' quoted, 'p' paren
	text string
}

// checkSQLExpr returns a short reason when expr can never be valid SQL, or "".
func checkSQLExpr(expr string) string {
	if strings.TrimSpace(expr) == "" {
		return "empty expression"
	}
	var toks []sqlTok
	depth := 0
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '-' && strings.HasPrefix(expr[i:], "--"):
			if j := strings.IndexByte(expr[i:], '\n'); j >= 0 {
				i += j
			} else {
				i = len(expr)
			}
		case c == '/' && strings.HasPrefix(expr[i:], "/*"):
			end, ok := skipBlockComment(expr, i)
			if !ok {
				return "unterminated comment"
			}
			i = end
		case c == '\'':
			end, ok := skipQuoted(expr, i, '\'', false)
			if !ok {
				return "unterminated string"
			}
			toks, i = append(toks, sqlTok{'q', ""}), end
		case c == '"':
			end, ok := skipQuoted(expr, i, '"', false)
			if !ok {
				return "unterminated quoted identifier"
			}
			toks, i = append(toks, sqlTok{'q', ""}), end
		case c == '$':
			if tag := dollarTag(expr[i:]); tag != "" {
				j := strings.Index(expr[i+len(tag):], tag)
				if j < 0 {
					return "unterminated dollar-quoted string"
				}
				toks, i = append(toks, sqlTok{'q', ""}), i+len(tag)+j+len(tag)
			} else {
				i++
			}
		case c == ';':
			return "semicolon is not allowed"
		case c == '(':
			depth++
			toks, i = append(toks, sqlTok{'p', "("}), i+1
		case c == ')':
			depth--
			if depth < 0 {
				return "unbalanced parentheses"
			}
			toks, i = append(toks, sqlTok{'p', ")"}), i+1
		case strings.IndexByte(sqlOpChars, c) >= 0:
			j := i
			for j < len(expr) && strings.IndexByte(sqlOpChars, expr[j]) >= 0 &&
				!strings.HasPrefix(expr[j:], "--") && !strings.HasPrefix(expr[j:], "/*") {
				j++
			}
			toks, i = append(toks, sqlTok{'o', expr[i:j]}), j
		case isSQLWordChar(c):
			j := i
			for j < len(expr) && isSQLWordChar(expr[j]) {
				j++
			}
			word := expr[i:j]
			if (word == "E" || word == "e") && j < len(expr) && expr[j] == '\'' {
				end, ok := skipQuoted(expr, j, '\'', true)
				if !ok {
					return "unterminated string"
				}
				toks, i = append(toks, sqlTok{'q', ""}), end
				continue
			}
			toks, i = append(toks, sqlTok{'w', strings.ToLower(word)}), j
		default:
			i++
		}
	}
	if depth != 0 {
		return "unbalanced parentheses"
	}
	return checkTokens(toks)
}

func checkTokens(toks []sqlTok) string {
	if len(toks) == 0 {
		return "empty expression"
	}
	isBool := func(t sqlTok) bool { return t.kind == 'w' && (t.text == "and" || t.text == "or") }
	if isBool(toks[0]) {
		return fmt.Sprintf("expression starts with %s", strings.ToUpper(toks[0].text))
	}
	if last := toks[len(toks)-1]; isBool(last) {
		return fmt.Sprintf("expression ends with %s", strings.ToUpper(last.text))
	}
	for i := 1; i < len(toks); i++ {
		a, b := toks[i-1], toks[i]
		if a.kind == 'o' && b.kind == 'o' && sqlComparisons[a.text] && sqlComparisons[b.text] {
			return fmt.Sprintf("two comparison operators in a row (%q)", a.text+" "+b.text)
		}
		if isBool(a) && isBool(b) {
			return fmt.Sprintf("%s followed by %s", strings.ToUpper(a.text), strings.ToUpper(b.text))
		}
	}
	return ""
}

func isSQLWordChar(c byte) bool {
	return c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// skipQuoted returns the index after the closing quote at s[start].
func skipQuoted(s string, start int, q byte, backslash bool) (int, bool) {
	for i := start + 1; i < len(s); i++ {
		switch {
		case backslash && s[i] == '\\':
			i++
		case s[i] == q:
			if i+1 < len(s) && s[i+1] == q {
				i++
				continue
			}
			return i + 1, true
		}
	}
	return 0, false
}

func skipBlockComment(s string, start int) (int, bool) {
	depth := 0
	for i := start; i < len(s)-1; i++ {
		switch s[i : i+2] {
		case "/*":
			depth++
			i++
		case "*/":
			depth--
			i++
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// dollarTag returns "$tag$" when s starts a dollar-quote opener, else "".
func dollarTag(s string) string {
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '$' {
			return s[:i+1]
		}
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (i == 1 || c < '0' || c > '9') {
			return ""
		}
	}
	return ""
}

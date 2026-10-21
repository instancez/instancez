package typegen

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

var tsTypes = map[string]string{
	"smallint": "number", "int2": "number", "int4": "number", "int8": "number",
	"float4": "number", "float8": "number", "integer": "number", "int": "number", "serial": "number",
	"smallserial": "number", "bigint": "number", "bigserial": "number", "real": "number",
	"double": "number", "double precision": "number", "float": "number",
	"numeric": "number", "decimal": "number",
	"boolean": "boolean", "bool": "boolean",
	"json": "Json", "jsonb": "Json",
	"text": "string", "varchar": "string", "character varying": "string", "char": "string",
	"character": "string", "uuid": "string", "date": "string", "timestamp": "string",
	"timestamp with time zone": "string", "timestamp without time zone": "string",
	"timestamptz": "string", "money": "string", "inet": "string", "cidr": "string",
	"macaddr": "string", "macaddr8": "string", "bytea": "string", "xml": "string",
	"point": "string", "tsquery": "string", "timetz": "string", "time with time zone": "string",
}

func tsType(sqlType string) string {
	t, isArray := strings.CutSuffix(strings.ToLower(strings.TrimSpace(sqlType)), "[]")
	t, _, _ = strings.Cut(t, "(")
	ts, ok := tsTypes[strings.TrimSpace(t)]
	if !ok {
		ts = "unknown"
	}
	if isArray {
		ts += "[]"
	}
	return ts
}

func isSerial(sqlType string) bool {
	switch strings.ToLower(strings.TrimSpace(sqlType)) {
	case "serial", "smallserial", "bigserial":
		return true
	}
	return false
}

func quoteString(s string) string {
	q := '"'
	if strings.Count(s, `"`) > strings.Count(s, `'`) {
		q = '\''
	}
	var b strings.Builder
	b.WriteRune(q)
	for _, r := range s {
		switch {
		case r == q || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(r) || r == '\u2028' || r == '\u2029':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteRune(q)
	return b.String()
}

var bareKey = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func key(name string) string {
	if bareKey.MatchString(name) {
		return name
	}
	return quoteString(name)
}

func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			n += 2
		default:
			if !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Me, r) {
				n++
			}
		}
	}
	return n
}

package typegen

import "testing"

func TestTSType(t *testing.T) {
	cases := map[string]string{
		"smallint": "number", "integer": "number", "int": "number", "serial": "number",
		"smallserial": "number", "bigint": "number", "bigserial": "number", "real": "number",
		"double": "number", "double precision": "number", "float": "number",
		"numeric": "number", "decimal": "number", "INTEGER": "number", "Double Precision": "number",
		"numeric(10,2)": "number", "numeric(10,2)[]": "number[]", "float(8)": "number",
		"boolean": "boolean", "bool": "boolean", "BOOLEAN": "boolean", "bool[]": "boolean[]",
		"json": "Json", "jsonb": "Json", "JSONB": "Json", "jsonb[]": "Json[]",
		"text": "string", "varchar": "string", "varchar(255)": "string", "character varying": "string",
		"character varying(10)": "string", "char": "string", "char(1)": "string", "character": "string",
		"uuid": "string", " uuid ": "string", "UUID": "string", "date": "string", "timestamp": "string",
		"timestamp with time zone": "string", "timestamp without time zone": "string",
		"timestamp(3)": "string", "timestamptz": "string", "money": "string",
		"inet": "string", "cidr": "string", "macaddr": "string", "bytea": "string", "xml": "string",
		"text[]": "string[]", " text[] ": "string[]", "uuid[]": "string[]",
		"interval": "unknown", "time": "unknown", "timetz": "unknown", "bit": "unknown",
		"bit varying": "unknown", "line": "unknown", "box": "unknown", "polygon": "unknown",
		"circle": "unknown", "point": "unknown", "tsvector": "unknown", "tsquery": "unknown",
		"interval[]": "unknown[]", "": "unknown", "[]": "unknown[]", "(": "unknown", "nonsense": "unknown",
		"text[][]": "unknown[]",
	}
	for in, want := range cases {
		if got := tsType(in); got != want {
			t.Errorf("tsType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSerial(t *testing.T) {
	cases := map[string]bool{
		"serial": true, "SERIAL": true, "smallserial": true, "bigserial": true, "BigSerial": true,
		"integer": false, "": false, "serial4": false, "serial[]": false,
	}
	for in, want := range cases {
		if got := isSerial(in); got != want {
			t.Errorf("isSerial(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestQuoteString(t *testing.T) {
	cases := map[string]string{
		"":          `""`,
		"abc":       `"abc"`,
		`a"b`:       `'a"b'`,
		`a'b`:       `"a'b"`,
		`a"b'c`:     `"a\"b'c"`,
		`a\b`:       `"a\\b"`,
		"a\nb":      `"a\nb"`,
		"a\rb\tc":   `"a\rb\tc"`,
		"a\u2028b":  `"a\u2028b"`,
		"a\u2029b":  `"a\u2029b"`,
		"\x01":      `"\u0001"`,
		"\x7f":      `"\u007f"`,
		`"'"`:       `'"\'"'`,
		`""'`:       `'""\''`,
		"日本😀":       `"日本😀"`,
		`'\`:        `"'\\"`,
		`a"b"c'd`:   `'a"b"c\'d'`,
		"\x00":      `"\u0000"`,
		"\x1f":      `"\u001f"`,
		"é":         `"é"`,
		"\u0080":    `"\u0080"`,
		`"\"`:       `'"\\"'`,
		"line1\nl2": `"line1\nl2"`,
	}
	for in, want := range cases {
		if got := quoteString(in); got != want {
			t.Errorf("quoteString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestKey(t *testing.T) {
	cases := map[string]string{
		"id": "id", "_x": "_x", "$a1": "$a1", "A_b": "A_b", "__proto__": "__proto__",
		"1a": `"1a"`, "a-b": `"a-b"`, "": `""`, "a b": `"a b"`, "é": `"é"`, `a"b`: `'a"b'`,
	}
	for in, want := range cases {
		if got := key(in); got != want {
			t.Errorf("key(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDisplayWidth(t *testing.T) {
	cases := map[string]int{
		"": 0, "abc": 3, "日本": 4, "😀": 2, "é": 1, "ａ": 2, "a日b": 4,
	}
	for in, want := range cases {
		if got := displayWidth(in); got != want {
			t.Errorf("displayWidth(%q) = %d, want %d", in, got, want)
		}
	}
}

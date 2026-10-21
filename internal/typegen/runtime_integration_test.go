//go:build integration

package typegen_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/testutil/dbboot"
	"github.com/instancez/instancez/internal/typegen"
)

// rtCol is the single source for the yaml, the generated-text check and the node assertions.
type rtCol struct {
	Col      string   `json:"col"`
	SQL      string   `json:"sql"`
	TS       string   `json:"ts"`
	JS       string   `json:"js"`
	Sample   any      `json:"sample"`
	Required bool     `json:"required"`
	Enum     []string `json:"enum,omitempty"`
}

var rtCols = []rtCol{
	{Col: "req_text", SQL: "text", TS: "string", Sample: "r", Required: true},
	{Col: "c_smallint", SQL: "smallint", TS: "number", Sample: 1},
	{Col: "c_integer", SQL: "integer", TS: "number", Sample: 2},
	{Col: "c_bigint", SQL: "bigint", TS: "number", Sample: 3},
	{Col: "c_real", SQL: "real", TS: "number", Sample: 1.5},
	{Col: "c_double", SQL: "double precision", TS: "number", Sample: 2.5},
	{Col: "c_numeric", SQL: "numeric(10,2)", TS: "number", Sample: 3.25},
	{Col: "c_bool", SQL: "boolean", TS: "boolean", Sample: true},
	{Col: "c_json", SQL: "json", TS: "Json", Sample: map[string]any{"a": 1}},
	{Col: "c_jsonb", SQL: "jsonb", TS: "Json", Sample: map[string]any{"b": []any{1, "x"}}},
	{Col: "c_text", SQL: "text", TS: "string", Sample: "t"},
	{Col: "c_varchar", SQL: "varchar(20)", TS: "string", Sample: "v"},
	{Col: "c_char", SQL: "char(2)", TS: "string", Sample: "c"},
	{Col: "c_uuid", SQL: "uuid", TS: "string", Sample: "11111111-1111-1111-1111-111111111111"},
	{Col: "c_date", SQL: "date", TS: "string", Sample: "2024-01-02"},
	{Col: "c_timestamp", SQL: "timestamp", TS: "string", Sample: "2024-01-02T03:04:05"},
	{Col: "c_timestamptz", SQL: "timestamptz", TS: "string", Sample: "2024-01-02T03:04:05Z"},
	{Col: "c_money", SQL: "money", TS: "string", Sample: "12.50"},
	{Col: "c_inet", SQL: "inet", TS: "string", Sample: "10.0.0.1"},
	{Col: "c_cidr", SQL: "cidr", TS: "string", Sample: "10.0.0.0/8"},
	{Col: "c_macaddr", SQL: "macaddr", TS: "string", Sample: "08:00:2b:01:02:03"},
	{Col: "c_bytea", SQL: "bytea", TS: "string", Sample: "\\x0102"},
	{Col: "c_xml", SQL: "xml", TS: "string", Sample: "<a/>"},
	{Col: "c_interval", SQL: "interval", TS: "unknown", JS: "object", Sample: "1 day"},
	{Col: "c_time", SQL: "time", TS: "unknown", JS: "object", Sample: "01:02:03"},
	{Col: "c_bit", SQL: "bit(3)", TS: "unknown", JS: "object", Sample: "101"},
	{Col: "c_point", SQL: "point", TS: "string", Sample: "(1,2)"},
	{Col: "c_tsvector", SQL: "tsvector", TS: "unknown", JS: "object", Sample: "a b"},
	{Col: "c_line", SQL: "line", TS: "unknown", JS: "object", Sample: "{1,2,3}"},
	{Col: "c_box", SQL: "box", TS: "unknown", JS: "object", Sample: "(1,2),(0,0)"},
	{Col: "c_polygon", SQL: "polygon", TS: "unknown", JS: "object", Sample: "((0,0),(1,1),(1,0))"},
	{Col: "c_circle", SQL: "circle", TS: "unknown", JS: "object", Sample: "<(0,0),1>"},
	{Col: "c_path", SQL: "path", TS: "unknown", JS: "object", Sample: "[(0,0),(1,1)]"},
	{Col: "c_lseg", SQL: "lseg", TS: "unknown", JS: "object", Sample: "[(0,0),(1,1)]"},
	{Col: "c_tsquery", SQL: "tsquery", TS: "string", Sample: "a & b"},
	{Col: "c_timetz", SQL: "timetz", TS: "string", Sample: "01:02:03+00"},
	{Col: "c_varbit", SQL: "bit varying(5)", TS: "unknown", JS: "object", Sample: "101"},
	{Col: "c_macaddr8", SQL: "macaddr8", TS: "string", Sample: "08:00:2b:01:02:03:04:05"},
	{Col: "c_int4range", SQL: "int4range", TS: "unknown", JS: "object", Sample: "[1,5)"},
	{Col: "c_text_arr", SQL: "text[]", TS: "string[]", Sample: []any{"a", "b"}},
	{Col: "c_int_arr", SQL: "integer[]", TS: "number[]", Sample: []any{1, 2}},
	{Col: "c_enum", SQL: "text", TS: `"one" | "two"`, Sample: "one", Enum: []string{"one", "two"}},
}

// jsKind is the runtime kind a generated type promises; "any" means the type is open.
func jsKind(ts string) string {
	switch {
	case strings.HasSuffix(ts, "[]"):
		return "array"
	case strings.HasPrefix(ts, `"`):
		return "string"
	case ts == "string" || ts == "number" || ts == "boolean":
		return ts
	}
	return "any"
}

const rtRPC = `
rpc:
  rt_void:
    returns: {type: void}
    body: BEGIN END;
  rt_scalar:
    returns: {type: integer}
    body: BEGIN RETURN 42; END;
  rt_setof_table:
    returns: {type: setof rt_all}
    body: BEGIN RETURN QUERY SELECT * FROM rt_all ORDER BY id; END;
  rt_setof_int:
    returns: {type: setof int}
    body: BEGIN RETURN QUERY SELECT 7; END;
  rt_table:
    returns: {type: "table(a int, b text)"}
    body: BEGIN RETURN QUERY SELECT 1, 'x'::text; END;
`

func rtYAML() string {
	var b strings.Builder
	b.WriteString("tables:\n  rt_all:\n    fields:\n      - {name: id, type: serial, primary_key: true}\n")
	for _, c := range rtCols {
		fmt.Fprintf(&b, "      - {name: %s, type: %q, required: %v", c.Col, c.SQL, c.Required)
		if len(c.Enum) > 0 {
			e, _ := json.Marshal(c.Enum)
			fmt.Fprintf(&b, ", enum: %s", e)
		}
		b.WriteString("}\n")
	}
	b.WriteString("  rt_parent:\n    fields:\n      - {name: id, type: serial, primary_key: true}\n")
	b.WriteString("  rt_child:\n    fields:\n      - {name: id, type: serial, primary_key: true}\n      - {name: parent_id, foreign_key: {references: rt_parent.id}}\n")
	b.WriteString("  rt_solo:\n    fields:\n      - {name: id, type: serial, primary_key: true}\n      - {name: parent_id, foreign_key: {references: rt_parent.id}, unique: true}\n")
	b.WriteString("  rt_audit:\n    schema: audit\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n      - {name: note, type: text}\n")
	return b.String() + rtRPC
}

func TestTypegenRuntime(t *testing.T) {
	dir := harnessDir(t)
	cfg, err := config.ParseBytes([]byte(rtYAML()), "runtime.yaml")
	if err != nil {
		t.Fatal(err)
	}

	text := string(typegen.Generate(cfg))
	for _, c := range rtCols {
		want := c.Col + ": " + c.TS
		if !c.Required {
			want += " | null"
		}
		if !strings.Contains(text, want+"\n") {
			t.Errorf("generated text lacks %q", want)
		}
	}
	for _, want := range []string{
		"rt_void: {\n        Args: Record<PropertyKey, never>\n        Returns: undefined\n      }",
		"rt_scalar: {\n        Args: Record<PropertyKey, never>\n        Returns: number | null\n      }",
		"rt_setof_table: {\n        Args: Record<PropertyKey, never>\n        Returns: Database[\"public\"][\"Tables\"][\"rt_all\"][\"Row\"][]\n      }",
		"rt_setof_int: {\n        Args: Record<PropertyKey, never>\n        Returns: {\n          rt_setof_int: number | null\n        }[]\n      }",
		"rt_table: {\n        Args: Record<PropertyKey, never>\n        Returns: {\n          a: number | null\n          b: string | null\n        }[]\n      }",
		"  audit: {\n    Tables: {\n      rt_audit: {",
		"isOneToOne: false",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated text lacks %q", want)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	owner, authDB := dbboot.StartContainer(t)
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	keys := app.NewJWTKeyManager(owner)
	if _, err := keys.Active(ctx); err != nil {
		t.Fatal(err)
	}
	pub := fmt.Sprintf("inz_publishable_%d", time.Now().UnixNano())
	sec := fmt.Sprintf("inz_secret_%d", time.Now().UnixNano())
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", pub)
	t.Setenv("INSTANCEZ_SECRET_KEY", sec)
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: authDB, DevMode: true, JWTKeys: keys,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for i := range rtCols {
		if rtCols[i].JS == "" {
			rtCols[i].JS = jsKind(rtCols[i].TS)
		}
	}
	cases, _ := json.Marshal(rtCols)
	cmd := exec.CommandContext(ctx, "node", "runtime.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "INZ_URL="+ts.URL, "INZ_KEY="+sec, "INZ_CASES="+string(cases))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("runtime.mjs: %v\n%s", err, out)
	}
}

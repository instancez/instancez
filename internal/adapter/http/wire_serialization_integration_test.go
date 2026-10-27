//go:build integration

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

var wireCols = []struct{ name, typ string }{
	{"c_integer", "integer"}, {"c_bigint", "bigint"}, {"c_smallint", "smallint"},
	{"c_text", "text"}, {"c_varchar", "varchar(10)"}, {"c_char", "char(2)"}, {"c_boolean", "boolean"},
	{"c_numeric", "numeric"}, {"c_real", "real"}, {"c_double", "double precision"},
	{"c_date", "date"}, {"c_time", "time"}, {"c_timetz", "timetz"}, {"c_timestamp", "timestamp"},
	{"c_timestamptz", "timestamptz"}, {"c_interval", "interval"},
	{"c_uuid", "uuid"}, {"c_jsonb", "jsonb"}, {"c_json", "json"}, {"c_bytea", "bytea"},
	{"c_inet", "inet"}, {"c_cidr", "cidr"}, {"c_macaddr", "macaddr"}, {"c_money", "money"},
	{"c_point", "point"}, {"c_line", "line"}, {"c_polygon", "polygon"}, {"c_circle", "circle"},
	{"c_box", "box"}, {"c_tsquery", "tsquery"}, {"c_tsvector", "tsvector"}, {"c_xml", "xml"},
	{"c_bit", "bit(3)"}, {"c_varbit", "bit varying(5)"},
	{"c_date_arr", "date[]"}, {"c_uuid_arr", "uuid[]"}, {"c_interval_arr", "interval[]"}, {"c_int_arr", "integer[]"},
}

var wireTypical = map[string]string{
	"id": "1", "parent_id": "1",
	"c_integer": "42", "c_bigint": "9007199254740993", "c_smallint": "7",
	"c_text": `'héllo "q" <a>'`, "c_varchar": "'abc'", "c_char": "'x'", "c_boolean": "true",
	"c_numeric": "123456789012345678901234567890.12345", "c_real": "1.5", "c_double": "3.141592653589793",
	"c_date": "'2024-01-02'", "c_time": "'03:04:05.678'", "c_timetz": "'03:04:05+02'",
	"c_timestamp": "'2024-01-02 03:04:05.123456'", "c_timestamptz": "'2024-01-02 03:04:05+00'",
	"c_interval": "'1 year 2 mons 3 days 04:05:06'",
	"c_uuid":     "'123e4567-e89b-12d3-a456-426614174000'",
	"c_jsonb":    `'{"n":12345678901234567890,"s":"2024-01-02T03:04:05Z"}'`,
	"c_json":     `'{"n":12345678901234567890,"s":"2024-01-02T03:04:05Z"}'`,
	"c_bytea":    `'\x0102ff'`, "c_inet": "'10.0.0.1/24'", "c_cidr": "'10.0.0.0/8'",
	"c_macaddr": "'08:00:2b:01:02:03'", "c_money": "'12.50'",
	"c_point": "'(1,2)'", "c_line": "'{1,2,3}'", "c_polygon": "'((0,0),(1,1),(1,0))'",
	"c_circle": "'<(0,0),1>'", "c_box": "'(1,2),(0,0)'", "c_tsquery": "'a & b'", "c_tsvector": "'a b'",
	"c_xml": "'<a/>'", "c_bit": "'101'", "c_varbit": "'10110'",
	"c_date_arr": "'{2024-01-02,infinity}'", "c_uuid_arr": "'{123e4567-e89b-12d3-a456-426614174000}'",
	"c_interval_arr": `'{"1 day","2 hours"}'`, "c_int_arr": "'{{1,2},{3,4}}'",
}

var wireEdge = map[string]string{
	"id": "2", "c_date": "'infinity'", "c_timestamp": "'-infinity'", "c_timestamptz": "'infinity'",
	"c_real": "'NaN'", "c_double": "'-Infinity'", "c_numeric": "'NaN'", "c_text": "''",
	"c_bytea": `''::bytea`, "c_jsonb": "'null'::jsonb", "c_date_arr": "'{}'",
}

func wireConfig() *domain.Config {
	fields := []domain.Field{
		{Name: "id", Type: "bigserial", PrimaryKey: true},
		{Name: "parent_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "wire_parent.id"}},
	}
	for _, c := range wireCols {
		fields = append(fields, domain.Field{Name: c.name, Type: c.typ})
	}
	fn := func(ret, cat, body string) domain.Function {
		return domain.Function{
			Language: "sql", Volatility: "stable", Security: "invoker",
			Returns: domain.FuncReturn{Type: ret}, ReturnCategory: cat, Body: body,
		}
	}
	return &domain.Config{Version: 1,
		Tables: map[string]domain.Table{
			"wire_all": {Fields: fields},
			"wire_parent": {Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text"},
				{Name: "big", Type: "bigint"},
				{Name: "amount", Type: "numeric(40,5)"},
			}},
		},
		RPC: map[string]domain.Function{
			"wire_setof": fn("setof wire_all", "setof", "SELECT * FROM wire_all ORDER BY id"),
			"wire_shape": fn("table(l lseg, r int4range)", "setof", "SELECT '[(0,0),(1,1)]'::lseg, '[1,5)'::int4range"),
			"wire_date":  fn("date", "scalar", "SELECT '2024-01-02'::date"),
			"wire_nan":   fn("double precision", "scalar", "SELECT 'NaN'::float8"),
			"wire_json":  fn("jsonb", "scalar", `SELECT '{"n":12345678901234567890}'::jsonb`),
		},
	}
}

func wireInsert(row map[string]string) string {
	cols := make([]string, 0, len(row))
	for k := range row {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	vals := make([]string, len(cols))
	for i, k := range cols {
		vals[i] = row[k]
	}
	return "INSERT INTO wire_all (" + strings.Join(cols, ",") + ") VALUES (" + strings.Join(vals, ",") + ")"
}

func wireDecode(t *testing.T, raw string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v), raw)
	return v
}

func TestWireSerialization(t *testing.T) {
	url := dbboot.StartRawContainer(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "ALTER DATABASE instancez_test SET timezone='Asia/Kolkata'")
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))

	owner, reqDB, err := dbboot.Bootstrap(ctx, url, domain.PoolConfig{Max: 4, Min: 1})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = owner.Close()
		_ = reqDB.Close()
	})
	cfg := wireConfig()
	require.NoError(t, app.NewMigrator(owner).Apply(ctx, cfg))

	_, err = owner.Exec(ctx, "INSERT INTO wire_parent (id, name, big, amount) VALUES (1, 'p1', 9007199254740993, 123456789012345678901234567890.12345)")
	require.NoError(t, err)
	for _, row := range []map[string]string{wireTypical, wireEdge, {"id": "3"}} {
		_, err = owner.Exec(ctx, wireInsert(row))
		require.NoError(t, err)
	}

	keys := app.NewJWTKeyManager(owner)
	_, err = keys.Active(ctx)
	require.NoError(t, err)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_wire")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_wire")
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: reqDB, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DevMode: true, JWTKeys: keys,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	call := func(method, path, body string, headers map[string]string) (int, http.Header, string) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("apikey", "inz_secret_wire")
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, resp.Header, string(raw)
	}
	rep := map[string]string{"Prefer": "return=representation"}

	want := func(col string, id any) any {
		t.Helper()
		rows, err := owner.Query(ctx, fmt.Sprintf("SELECT coalesce(to_json(%s)::text,'null') AS v FROM wire_all WHERE id=$1", col), id)
		require.NoError(t, err)
		require.Len(t, rows, 1, "%s id=%v", col, id)
		return wireDecode(t, rows[0]["v"].(string))
	}
	allCols := []string{"parent_id"}
	for _, c := range wireCols {
		allCols = append(allCols, c.name)
	}
	assertRow := func(row map[string]any, id int64) {
		t.Helper()
		for _, col := range allCols {
			assert.Equal(t, want(col, id), row[col], "id=%d col=%s", id, col)
		}
	}
	rowsOf := func(raw string) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, r := range wireDecode(t, raw).([]any) {
			out = append(out, r.(map[string]any))
		}
		return out
	}
	idOf := func(row map[string]any) int64 {
		n, err := row["id"].(json.Number).Int64()
		require.NoError(t, err)
		return n
	}

	t.Run("list matches to_json", func(t *testing.T) {
		code, h, raw := call("GET", "/rest/v1/wire_all?order=id.desc", "", map[string]string{"Range": "0-1"})
		require.Equal(t, 200, code, raw)
		assert.Equal(t, "0-1/*", h.Get("Content-Range"))
		rows := rowsOf(raw)
		require.Len(t, rows, 2)
		assert.EqualValues(t, 3, idOf(rows[0]))
		assert.EqualValues(t, 2, idOf(rows[1]))
		for _, r := range rows {
			assertRow(r, idOf(r))
		}
	})

	t.Run("typical row has exact and tz-shifted values", func(t *testing.T) {
		code, _, raw := call("GET", "/rest/v1/wire_all?id=eq.1", "", nil)
		require.Equal(t, 200, code, raw)
		row := rowsOf(raw)[0]
		assertRow(row, 1)
		assert.True(t, strings.HasSuffix(row["c_timestamptz"].(string), "+05:30"), row["c_timestamptz"])
		assert.Equal(t, json.Number("9007199254740993"), row["c_bigint"])
		assert.Equal(t, "2024-01-02", row["c_date"])
		assert.Equal(t, "infinity", rowsOf(mustGet(t, call, "/rest/v1/wire_all?id=eq.2"))[0]["c_date"])
		assert.Equal(t, "NaN", rowsOf(mustGet(t, call, "/rest/v1/wire_all?id=eq.2"))[0]["c_real"])
	})

	t.Run("single object", func(t *testing.T) {
		code, _, raw := call("GET", "/rest/v1/wire_all?id=eq.1", "", map[string]string{"Accept": "application/vnd.pgrst.object+json"})
		require.Equal(t, 200, code, raw)
		assertRow(wireDecode(t, raw).(map[string]any), 1)
	})

	t.Run("csv keeps arrays as json", func(t *testing.T) {
		code, _, raw := call("GET", "/rest/v1/wire_all?id=eq.1&select=id,c_int_arr,c_jsonb", "", map[string]string{"Accept": "text/csv"})
		require.Equal(t, 200, code, raw)
		assert.Contains(t, raw, `"[[1,2],[3,4]]"`)
		assert.Contains(t, raw, "12345678901234567890")
	})

	t.Run("cast and aggregate", func(t *testing.T) {
		code, _, raw := call("GET", "/rest/v1/wire_all?id=eq.1&select=c_date::text,c_timestamptz.max()", "", nil)
		require.Equal(t, 200, code, raw)
		row := rowsOf(raw)[0]
		assert.Equal(t, "2024-01-02", row["c_date"])
		rows, err := owner.Query(ctx, "SELECT to_json(max(c_timestamptz))::text AS v FROM wire_all WHERE id=1")
		require.NoError(t, err)
		assert.Equal(t, wireDecode(t, rows[0]["v"].(string)), row["max"])
	})

	t.Run("embeds", func(t *testing.T) {
		code, _, raw := call("GET", "/rest/v1/wire_all?id=eq.1&select=id,...wire_parent(name)", "", nil)
		require.Equal(t, 200, code, raw)
		assert.JSONEq(t, `[{"id":1,"name":"p1"}]`, raw)

		code, _, raw = call("GET", "/rest/v1/wire_all?id=eq.1&select=id,wire_parent(big,amount)", "", nil)
		require.Equal(t, 200, code, raw)
		parent := rowsOf(raw)[0]["wire_parent"].(map[string]any)
		rows, err := owner.Query(ctx, "SELECT to_json(big)::text AS b, to_json(amount)::text AS a FROM wire_parent WHERE id=1")
		require.NoError(t, err)
		assert.Equal(t, wireDecode(t, rows[0]["b"].(string)), parent["big"])
		assert.Equal(t, wireDecode(t, rows[0]["a"].(string)), parent["amount"])
		assert.Equal(t, json.Number("123456789012345678901234567890.12345"), parent["amount"])
	})

	t.Run("alias named like the wrapper", func(t *testing.T) {
		code, _, raw := call("GET", "/rest/v1/wire_all?id=eq.1&select=_inz_r:id", "", nil)
		require.Equal(t, 200, code, raw)
		assert.JSONEq(t, `[{"_inz_r":1}]`, raw)
	})

	t.Run("insert upsert patch delete", func(t *testing.T) {
		body := `{"id":10,"c_integer":5,"c_date":"2024-03-04","c_timestamptz":"2024-03-04T00:00:00Z","c_numeric":"12.50","c_bytea":"\\x0a0b","c_real":"NaN"}`
		code, _, raw := call("POST", "/rest/v1/wire_all", body, rep)
		require.Equal(t, 201, code, raw)
		rows := rowsOf(raw)
		require.Len(t, rows, 1)
		assertRow(rows[0], 10)
		assert.Equal(t, "2024-03-04", rows[0]["c_date"])
		assert.True(t, strings.HasSuffix(rows[0]["c_timestamptz"].(string), "+05:30"), rows[0]["c_timestamptz"])
		assert.Equal(t, "NaN", rows[0]["c_real"])

		code, _, raw = call("PUT", "/rest/v1/wire_all", `{"id":10,"c_integer":6,"c_date":"2024-03-05"}`, rep)
		require.Equal(t, 200, code, raw)
		rows = rowsOf(raw)
		require.Len(t, rows, 1)
		assertRow(rows[0], 10)
		assert.Equal(t, "2024-03-05", rows[0]["c_date"])

		code, _, raw = call("PATCH", "/rest/v1/wire_all?id=eq.10", `{"c_integer":7}`, rep)
		require.Equal(t, 200, code, raw)
		rows = rowsOf(raw)
		require.Len(t, rows, 1)
		assertRow(rows[0], 10)

		code, _, raw = call("PATCH", "/rest/v1/wire_all?id=eq.10", `{"c_integer":8}`, map[string]string{
			"Prefer": "return=representation", "Accept": "application/vnd.pgrst.object+json",
		})
		require.Equal(t, 200, code, raw)
		assertRow(wireDecode(t, raw).(map[string]any), 10)

		code, _, raw = call("PATCH", "/rest/v1/wire_all?id=eq.-1", `{"c_integer":1}`, rep)
		require.Equal(t, 200, code, raw)
		assert.JSONEq(t, `[]`, raw)

		before := map[string]any{}
		for _, col := range allCols {
			before[col] = want(col, 10)
		}
		code, _, raw = call("DELETE", "/rest/v1/wire_all?id=eq.10", "", rep)
		require.Equal(t, 200, code, raw)
		rows = rowsOf(raw)
		require.Len(t, rows, 1)
		for _, col := range allCols {
			assert.Equal(t, before[col], rows[0][col], col)
		}
	})

	t.Run("rpc setof with count", func(t *testing.T) {
		code, h, raw := call("POST", "/rest/v1/rpc/wire_setof?limit=1", "{}", map[string]string{"Prefer": "count=exact"})
		require.Equal(t, 206, code, raw)
		assert.Equal(t, "0-0/3", h.Get("Content-Range"))
		rows := rowsOf(raw)
		require.Len(t, rows, 1)
		assertRow(rows[0], 1)
	})

	t.Run("rpc shapes", func(t *testing.T) {
		code, _, raw := call("POST", "/rest/v1/rpc/wire_shape", "{}", nil)
		require.Equal(t, 200, code, raw)
		assert.JSONEq(t, `[{"l":"[(0,0),(1,1)]","r":"[1,5)"}]`, raw)

		code, _, raw = call("POST", "/rest/v1/rpc/wire_date", "{}", nil)
		require.Equal(t, 200, code, raw)
		assert.JSONEq(t, `"2024-01-02"`, raw)

		code, _, raw = call("POST", "/rest/v1/rpc/wire_nan", "{}", nil)
		require.Equal(t, 200, code, raw)
		assert.JSONEq(t, `"NaN"`, raw)

		code, _, raw = call("POST", "/rest/v1/rpc/wire_json", "{}", nil)
		require.Equal(t, 200, code, raw)
		assert.Equal(t, `{"n":12345678901234567890}`, raw)
	})
}

func mustGet(t *testing.T, call func(method, path, body string, headers map[string]string) (int, http.Header, string), path string) string {
	t.Helper()
	code, _, raw := call("GET", path, "", nil)
	require.Equal(t, 200, code, raw)
	return raw
}

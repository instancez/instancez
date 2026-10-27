package http

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/instancez/instancez/internal/domain"
)

type wireFakeTx struct {
	domain.Tx
	gotSQL  string
	gotArgs []any
	rows    []map[string]any
	err     error
}

func (f *wireFakeTx) Query(_ context.Context, q string, args ...any) ([]map[string]any, error) {
	f.gotSQL, f.gotArgs = q, args
	return f.rows, f.err
}

func TestWireRowsSQL_ExactForm(t *testing.T) {
	assert.Equal(t,
		"WITH _inz_r AS (SELECT 1 AS _inz_r) SELECT row_to_json(_inz_r.*)::text AS _inz_row FROM _inz_r",
		wireRowsSQL("SELECT 1 AS _inz_r"))
}

func TestQueryWireRows_DecodesWithUseNumber(t *testing.T) {
	const in = `{"a":9007199254740993,"n":123456789012345678901234567890.5,"z":null,"j":{"k":[1,"x"]}}`
	tx := &wireFakeTx{rows: []map[string]any{{"_inz_row": in}}}

	rows, err := queryWireRows(context.Background(), tx, "SELECT $1, $2", 7, "x")
	require.NoError(t, err)
	assert.Equal(t, wireRowsSQL("SELECT $1, $2"), tx.gotSQL)
	assert.Equal(t, []any{7, "x"}, tx.gotArgs)
	require.Len(t, rows, 1)
	assert.Equal(t, json.Number("9007199254740993"), rows[0]["a"])
	assert.Equal(t, json.Number("123456789012345678901234567890.5"), rows[0]["n"])
	assert.Nil(t, rows[0]["z"])
	assert.Contains(t, rows[0], "z")
	j, ok := rows[0]["j"].(map[string]any)
	require.True(t, ok)
	assert.IsType(t, []any{}, j["k"])

	out, err := json.Marshal(rows[0])
	require.NoError(t, err)
	assert.JSONEq(t, in, string(out))
}

func TestQueryWireRows_ZeroRowsNonNil(t *testing.T) {
	rows, err := queryWireRows(context.Background(), &wireFakeTx{}, "SELECT 1")
	require.NoError(t, err)
	require.NotNil(t, rows)
	assert.Empty(t, rows)
}

func TestQueryWireRows_BadCells(t *testing.T) {
	cases := map[string]map[string]any{
		"missing":      {"other": "{}"},
		"wrong type":   {"_inz_row": int64(1)},
		"bad json":     {"_inz_row": "{bad"},
		"nil cell":     {"_inz_row": nil},
		"empty string": {"_inz_row": ""},
		"trailing":     {"_inz_row": `{"a":1} {"b":2}`},
		"null row":     {"_inz_row": "null"},
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := queryWireRows(context.Background(), &wireFakeTx{rows: []map[string]any{row}}, "SELECT 1")
			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), "decode wire row:"), err.Error())
		})
	}
}

func TestQueryWireRows_BadJSONWrapsCause(t *testing.T) {
	_, err := queryWireRows(context.Background(), &wireFakeTx{rows: []map[string]any{{"_inz_row": "{bad"}}}, "SELECT 1")
	require.Error(t, err)
	var syn *json.SyntaxError
	assert.ErrorAs(t, err, &syn)
	assert.NotNil(t, errors.Unwrap(err))
}

func TestQueryWireRows_QueryErrorPassesThrough(t *testing.T) {
	sentinel := errors.New("boom")
	_, err := queryWireRows(context.Background(), &wireFakeTx{err: sentinel}, "SELECT 1")
	assert.ErrorIs(t, err, sentinel)
}

func TestQueryWireRaw_KeepsBytesAndOrder(t *testing.T) {
	const a, b = `{"z":1,"a":9007199254740993,"n":"NaN"}`, `{"j":{"y":1,"x":[1.50]}}`
	tx := &wireFakeTx{rows: []map[string]any{{"_inz_row": a}, {"_inz_row": b}}}
	rows, err := queryWireRaw(context.Background(), tx, "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, wireRowsSQL("SELECT 1"), tx.gotSQL)
	require.Len(t, rows, 2)
	assert.Equal(t, a, string(rows[0]))
	assert.Equal(t, `[`+a+`,`+b+`]`, string(joinWireRows(rows)))
}

func TestJoinWireRows_EmptyIsArray(t *testing.T) {
	assert.Equal(t, "[]", string(joinWireRows(nil)))
	rows, err := queryWireRaw(context.Background(), &wireFakeTx{}, "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, "[]", string(joinWireRows(rows)))
}

func TestQueryWireRaw_BadCells(t *testing.T) {
	for name, cell := range map[string]any{
		"nil": nil, "int": int64(1), "empty": "", "null": "null", "array": "[1]", "string": `"x"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := queryWireRaw(context.Background(), &wireFakeTx{rows: []map[string]any{{"_inz_row": cell}}}, "SELECT 1")
			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), "decode wire row:"), err.Error())
		})
	}
}

func TestSplitRawRPCTotal_NoHTMLEscape(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"s":"<a> & b","__inz_total":3,"__inz_row":true,"__inz_rn":1}`)}
	out, total, err := splitRawRPCTotal(raw)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, out, 1)
	assert.Equal(t, `{"s":"<a> & b"}`, string(out[0]))

	_, _, err = splitRawRPCTotal([]json.RawMessage{json.RawMessage("{bad")})
	require.Error(t, err)
}

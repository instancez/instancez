package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callHandleDBError drives handleDBError through a throwaway gin context and
// returns the status code plus the decoded error envelope.
func callHandleDBError(t *testing.T, err error) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleDBError(c, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body
}

// A nested object for a scalar column (e.g. {"is_completed":{"not":false}})
// fails inside pgx before it reaches Postgres, so it never carries a SQLSTATE.
// It's still bad client input and must surface as a 4xx, not a 500.
func TestHandleDBError_EncodeFailureIsClientError(t *testing.T) {
	// Reproduce the exact error pgx raises so this test also breaks if pgx
	// ever changes the wording our substring match depends on.
	_, err := pgtype.NewMap().Encode(pgtype.BoolOID, pgtype.TextFormatCode,
		map[string]any{"not": false}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot find encode plan")

	status, body := callHandleDBError(t, err)
	assert.Equal(t, 400, status)
	assert.Equal(t, "22P02", body["code"])
	assert.NotEmpty(t, body["message"])
	for _, k := range []string{"code", "message", "details", "hint"} {
		_, ok := body[k]
		assert.True(t, ok, "missing %q key", k)
	}
}

// A genuinely unknown error still gets the generic 500 — the new arm must not
// swallow real server faults.
func TestHandleDBError_UnknownErrorStays500(t *testing.T) {
	status, body := callHandleDBError(t, errors.New("something exploded"))
	assert.Equal(t, 500, status)
	assert.Equal(t, "XX000", body["code"])
}

func TestHandleDBError_GroupingErrorIs400(t *testing.T) {
	status, body := callHandleDBError(t, &pgconn.PgError{Code: "42803", Message: `column "x" must appear in the GROUP BY clause`})
	assert.Equal(t, 400, status)
	assert.Equal(t, "42803", body["code"])
}

func TestHandleDBError_StatusMatchesPostgREST(t *testing.T) {
	cases := []struct {
		code, msg string
		want      int
	}{
		{"23502", "", 400}, {"23514", "", 400}, {"22P02", "", 400}, {"22001", "", 400},
		{"22003", "", 400}, {"22007", "", 400}, {"22023", "", 400}, {"42703", "", 400},
		{"42601", "", 400}, {"42803", "", 400}, {"42602", "", 400}, {"23000", "", 400},
		{"23503", "", 409}, {"23505", "", 409},
		{"P0001", "", 400}, {"P0002", "", 500}, {"P0003", "", 500},
		{"42501", "", 403}, {"42P01", "", 404}, {"42883", "", 404}, {"42P17", "", 500},
		{"25006", "", 405}, {"25P02", "", 500},
		{"21000", "DELETE requires a WHERE clause", 400}, {"21000", "more than one row", 500},
		{"08006", "", 503}, {"53300", "", 503}, {"53400", "", 500},
		{"54000", "", 500}, {"55000", "", 500}, {"57014", "", 500}, {"57P01", "", 503},
		{"0P000", "", 403}, {"28000", "", 403}, {"40001", "", 500}, {"XX000", "", 500},
		{"PT404", "gone", 404}, {"PTabc", "", 500},
		{"99999", "", 400}, {"", "", 400},
	}
	for _, tc := range cases {
		t.Run(tc.code+"/"+tc.msg, func(t *testing.T) {
			status, body := callHandleDBError(t, &pgconn.PgError{Code: tc.code, Message: tc.msg})
			assert.Equal(t, tc.want, status)
			assert.Equal(t, tc.code, body["code"])
		})
	}
}

func TestHandleDBError_WrappedPgError(t *testing.T) {
	err := fmt.Errorf("exec: %w", &pgconn.PgError{Code: "23505", Message: "dup"})
	status, body := callHandleDBError(t, err)
	assert.Equal(t, 409, status)
	assert.Equal(t, "23505", body["code"])
}

func TestHandleDBError_NonPgFallbacksAre400(t *testing.T) {
	for msg, code := range map[string]string{
		"violates foreign key": "23503", "violates check": "23514",
		"not-null constraint": "23502", "invalid input syntax": "22P02", "value too long": "22001",
	} {
		status, body := callHandleDBError(t, errors.New(msg))
		want := 400
		if code == "23503" {
			want = 409
		}
		assert.Equal(t, want, status, msg)
		assert.Equal(t, code, body["code"])
	}
}

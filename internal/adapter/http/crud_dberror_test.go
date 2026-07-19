package http

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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
	assert.Equal(t, 422, status)
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

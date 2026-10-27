package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/instancez/instancez/internal/domain"
)

// wireRowsSQL makes Postgres render each row as JSON text; sql must not hold a data-modifying CTE.
func wireRowsSQL(sql string) string {
	return "WITH _inz_r AS (" + sql + ") SELECT row_to_json(_inz_r.*)::text AS _inz_row FROM _inz_r"
}

func queryWireRaw(ctx context.Context, tx domain.Tx, sql string, args ...any) ([]json.RawMessage, error) {
	// nosemgrep -- sql is built from validated identifiers; values are bound args
	raw, err := tx.Query(ctx, wireRowsSQL(sql), args...)
	if err != nil {
		return nil, err
	}
	rows := make([]json.RawMessage, 0, len(raw))
	for _, r := range raw {
		text, _ := r["_inz_row"].(string)
		if !strings.HasPrefix(text, "{") {
			return nil, errors.New("decode wire row: cell is not a JSON object")
		}
		rows = append(rows, json.RawMessage(text))
	}
	return rows, nil
}

func decodeWireRows(raw []json.RawMessage) ([]map[string]any, error) {
	rows := make([]map[string]any, 0, len(raw))
	for _, text := range raw {
		dec := json.NewDecoder(bytes.NewReader(text))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("decode wire row: %w", err)
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return nil, errors.New("decode wire row: trailing data")
		}
		rows = append(rows, m)
	}
	return rows, nil
}

func queryWireRows(ctx context.Context, tx domain.Tx, sql string, args ...any) ([]map[string]any, error) {
	raw, err := queryWireRaw(ctx, tx, sql, args...)
	if err != nil {
		return nil, err
	}
	return decodeWireRows(raw)
}

func joinWireRows(rows []json.RawMessage) []byte {
	n := 2
	for _, r := range rows {
		n += len(r) + 1
	}
	buf := make([]byte, 0, n)
	buf = append(buf, '[')
	for i, r := range rows {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, r...)
	}
	return append(buf, ']')
}

func writeWireRows(c *gin.Context, status int, rows []json.RawMessage) {
	c.Data(status, "application/json; charset=utf-8", joinWireRows(rows))
}

func writeWireRow(c *gin.Context, status int, row json.RawMessage) {
	c.Data(status, "application/json; charset=utf-8", row)
}

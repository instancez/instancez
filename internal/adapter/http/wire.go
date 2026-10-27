package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// wireRowsSQL makes Postgres render each row as JSON text; sql must not hold a data-modifying CTE.
func wireRowsSQL(sql string) string {
	return "WITH _inz_r AS (" + sql + ") SELECT row_to_json(_inz_r.*)::text AS _inz_row FROM _inz_r"
}

func queryWireRows(ctx context.Context, tx domain.Tx, sql string, args ...any) ([]map[string]any, error) {
	// nosemgrep -- sql is built from validated identifiers; values are bound args
	raw, err := tx.Query(ctx, wireRowsSQL(sql), args...)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		text, ok := r["_inz_row"].(string)
		if !ok {
			return nil, fmt.Errorf("decode wire row: _inz_row is %T, want string", r["_inz_row"])
		}
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("decode wire row: %w", err)
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("decode wire row: trailing data")
		}
		rows = append(rows, m)
	}
	return rows, nil
}

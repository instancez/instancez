package http

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/instancez/instancez/internal/csvutil"
	"github.com/instancez/instancez/internal/domain"
)

func contentTypeIsCSV(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return false
	}
	if i := strings.Index(ct, ";"); i != -1 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "text/csv"
}

func acceptsCSV(accept string) bool {
	accept = strings.ToLower(accept)
	for _, part := range strings.Split(accept, ",") {
		part = strings.TrimSpace(part)
		if idx := strings.Index(part, ";"); idx != -1 {
			part = strings.TrimSpace(part[:idx])
		}
		if part == "text/csv" {
			return true
		}
	}
	return false
}

func csvReadRecords(body []byte) ([]map[string]any, error) {
	return csvutil.ReadRecords(body)
}

func csvCoerceRecords(records []map[string]any, table domain.Table) []map[string]any {
	return csvutil.CoerceRecords(records, table)
}

func coerceCSVValue(s string, pgType string) (any, bool) {
	return csvutil.CoerceValue(s, pgType)
}

func csvRenderRows(rows []map[string]any) ([]byte, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	cols := unionColumns(rows)
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(cols); err != nil {
		return nil, err
	}
	for _, row := range rows {
		line := make([]string, len(cols))
		for i, col := range cols {
			if v, ok := row[col]; ok && v != nil {
				line[i] = fmt.Sprintf("%v", v)
			}
		}
		if err := w.Write(line); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

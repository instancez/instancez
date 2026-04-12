package http

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// contentTypeIsCSV returns true when the Content-Type header designates a
// CSV request body. Charset suffixes and case differences are tolerated.
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

// acceptsCSV returns true when the Accept header mentions text/csv. We do
// not attempt full content negotiation — a simple substring check is
// sufficient because Accept values are short and client-controlled.
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

// csvReadRecords parses a CSV request body into a list of records. The
// first non-empty row is treated as the header; subsequent rows become
// maps keyed by header name. Values are always strings — Postgres will
// coerce on insert.
func csvReadRecords(body []byte) ([]map[string]any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty CSV body")
	}
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1 // allow ragged rows
	header, err := r.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("empty CSV body")
		}
		return nil, fmt.Errorf("csv header: %w", err)
	}
	var out []map[string]any
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv row: %w", err)
		}
		rec := make(map[string]any, len(header))
		for i, col := range header {
			if i < len(row) {
				rec[col] = row[i]
			} else {
				rec[col] = ""
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

// csvRenderRows emits rows as CSV with a header row drawn from the union
// of keys (sorted). Missing cells are empty strings. Values are stringified
// with fmt.Sprintf so numbers, bools, and strings all round-trip.
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

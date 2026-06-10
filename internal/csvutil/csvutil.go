package csvutil

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/saedx1/instancez/internal/domain"
)

// ReadRecords parses a CSV body into a list of records. The first non-empty
// row is treated as the header; subsequent rows become maps keyed by header
// name. Values are always strings.
func ReadRecords(body []byte) ([]map[string]any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty CSV body")
	}
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
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

// CoerceRecords converts string values in CSV-parsed records to the Go types
// that match the table's declared column types.
func CoerceRecords(records []map[string]any, table domain.Table) []map[string]any {
	for _, rec := range records {
		for col, val := range rec {
			s, ok := val.(string)
			if !ok {
				continue
			}
			field, exists := table.GetField(col)
			if !exists {
				continue
			}
			coerced, changed := CoerceValue(s, field.Type)
			if changed {
				rec[col] = coerced
			}
		}
	}
	return records
}

// CoerceValue converts a string value to the appropriate Go type based on the
// Postgres column type. Returns (value, true) if a conversion was made.
func CoerceValue(s string, pgType string) (any, bool) {
	base := strings.ToLower(strings.TrimSpace(pgType))
	if idx := strings.Index(base, "("); idx != -1 {
		base = strings.TrimSpace(base[:idx])
	}
	isArray := strings.HasSuffix(base, "[]")
	if isArray {
		base = strings.TrimSuffix(base, "[]")
	}

	if s == "" {
		switch base {
		case "text", "varchar", "character varying", "char", "character", "bpchar", "citext", "name":
			return s, false
		default:
			return nil, true
		}
	}

	switch base {
	case "integer", "int", "int4", "smallint", "int2":
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v, true
		}
	case "bigint", "int8", "bigserial", "serial8":
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v, true
		}
	case "serial", "serial4":
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v, true
		}
	case "real", "float4":
		if v, err := strconv.ParseFloat(s, 32); err == nil {
			return v, true
		}
	case "double precision", "float8", "float":
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v, true
		}
	case "numeric", "decimal":
		return s, false
	case "boolean", "bool":
		switch strings.ToLower(s) {
		case "true", "t", "yes", "y", "1", "on":
			return true, true
		case "false", "f", "no", "n", "0", "off":
			return false, true
		}
	case "json", "jsonb":
		if json.Valid([]byte(s)) {
			return s, false
		}
	}
	return s, false
}

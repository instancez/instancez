package postgrest

import (
	"github.com/instancez/instancez/internal/csvutil"
	"github.com/instancez/instancez/internal/domain"
)

// CsvReadRecords parses CSV bytes into a slice of string-keyed maps.
func CsvReadRecords(body []byte) ([]map[string]any, error) {
	return csvutil.ReadRecords(body)
}

// CsvCoerceRecords applies Postgres-type coercion to CSV-parsed string values.
func CsvCoerceRecords(records []map[string]any, table domain.Table) []map[string]any {
	return csvutil.CoerceRecords(records, table)
}

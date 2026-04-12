package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/saedx1/ultrabase/internal/domain"
)

// existingColumn represents a column from information_schema.
type existingColumn struct {
	Name     string
	DataType string
	Nullable bool
	Default  string
}

// introspectTable queries information_schema for columns of a given table.
func (m *Migrator) introspectTable(ctx context.Context, tableName string) ([]existingColumn, error) {
	rows, err := m.db.Query(ctx,
		`SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		 FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1
		 ORDER BY ordinal_position`, tableName)
	if err != nil {
		return nil, err
	}

	var cols []existingColumn
	for _, row := range rows {
		col := existingColumn{
			Name:     fmt.Sprint(row["column_name"]),
			DataType: fmt.Sprint(row["data_type"]),
			Nullable: fmt.Sprint(row["is_nullable"]) == "YES",
			Default:  fmt.Sprint(row["column_default"]),
		}
		cols = append(cols, col)
	}
	return cols, nil
}

// tableExists checks if a table exists in the public schema.
func (m *Migrator) tableExists(ctx context.Context, tableName string) (bool, error) {
	row, err := m.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = $1) AS exists`, tableName)
	if err != nil {
		return false, err
	}
	return row["exists"] == true, nil
}

// diffTable generates ALTER TABLE statements to bring an existing table in sync with the config.
func diffTable(tableName string, existing []existingColumn, desired domain.Table) []string {
	var ddl []string

	// Build lookup of existing columns
	existingMap := make(map[string]existingColumn)
	for _, col := range existing {
		existingMap[col.Name] = col
	}

	// Check for new columns (in config but not in DB)
	for fieldName, field := range desired.Fields {
		if _, exists := existingMap[fieldName]; !exists {
			colDef := formatColumn(fieldName, field)
			ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", tableName, colDef))
		}
	}

	// Check for columns that need type changes or nullability changes
	for fieldName, field := range desired.Fields {
		existing, exists := existingMap[fieldName]
		if !exists {
			continue // already handled as new column
		}

		fieldType := field.Type
		if fieldType == "" && field.ForeignKey != nil {
			fieldType = "BIGINT"
		}
		desiredType := normalizeType(fieldType)
		existingType := normalizeInformationSchemaType(existing.DataType)

		// Type change
		if desiredType != existingType && !isCompatibleType(existingType, desiredType) {
			ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;",
				tableName, fieldName, fieldType))
		}

		// Nullability change
		desiredNullable := !field.Required && !field.PrimaryKey
		if existing.Nullable != desiredNullable {
			if desiredNullable {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;",
					tableName, fieldName))
			} else {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;",
					tableName, fieldName))
			}
		}
	}

	return ddl
}

// normalizeType maps config types to a comparable form.
func normalizeType(t string) string {
	t = strings.ToLower(t)
	switch {
	case t == "bigserial":
		return "bigint" // bigserial is stored as bigint
	case t == "serial":
		return "integer"
	case strings.HasPrefix(t, "varchar"):
		return "varchar"
	case t == "bool":
		return "boolean"
	case t == "timestamptz":
		return "timestamptz"
	case t == "int":
		return "integer"
	}
	return t
}

// normalizeInformationSchemaType maps information_schema types to our type system.
func normalizeInformationSchemaType(t string) string {
	t = strings.ToLower(t)
	switch {
	case t == "bigint":
		return "bigint"
	case t == "integer":
		return "integer"
	case t == "character varying":
		return "varchar"
	case t == "text":
		return "text"
	case t == "boolean":
		return "boolean"
	case t == "timestamp with time zone":
		return "timestamptz"
	case t == "timestamp without time zone":
		return "timestamp"
	case t == "uuid":
		return "uuid"
	case t == "jsonb":
		return "jsonb"
	case t == "json":
		return "json"
	case t == "double precision":
		return "double precision"
	case t == "real":
		return "real"
	case t == "smallint":
		return "smallint"
	case strings.HasPrefix(t, "numeric"):
		return "numeric"
	case t == "date":
		return "date"
	case t == "array":
		return "array"
	}
	return t
}

// isCompatibleType checks if two types are compatible (e.g., serial stored as integer).
func isCompatibleType(existing, desired string) bool {
	// These pairs are equivalent
	compatible := map[string]string{
		"bigint":  "bigint",
		"integer": "integer",
	}
	return compatible[existing] == compatible[desired]
}

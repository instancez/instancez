package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// configDiff holds the DDL statements needed to reconcile the old config with
// the new one. Removal DDL runs before creation DDL in Plan so that renamed
// objects don't collide.
type configDiff struct {
	Removals    []string // DROP TABLE, DROP COLUMN, DROP INDEX, DROP POLICY, DROP FUNCTION
	Additions   []string // CREATE TABLE, ALTER TABLE ADD COLUMN, CREATE INDEX, CREATE POLICY
	Alterations []string // ALTER TABLE ALTER COLUMN TYPE, SET/DROP NOT NULL
}

// diffConfigs compares an old config (from the last migration) against the new
// config and returns DDL statements for objects that were removed, added, or changed.
func diffConfigs(old, new *domain.Config) configDiff {
	var diff configDiff

	if old == nil {
		return diff
	}

	// Removals (order: policies → indexes → columns → tables → storage → functions → search)
	diff.Removals = append(diff.Removals, diffRemovedRLSPolicies(old, new)...)
	diff.Removals = append(diff.Removals, diffRemovedIndexes(old, new)...)
	diff.Removals = append(diff.Removals, diffRemovedColumns(old, new)...)
	diff.Removals = append(diff.Removals, diffRemovedTables(old, new)...)
	diff.Removals = append(diff.Removals, diffRemovedStorageRLS(old, new)...)
	diff.Removals = append(diff.Removals, diffRemovedRPCFunctions(old, new)...)
	diff.Removals = append(diff.Removals, diffRemovedSearch(old, new)...)

	// Additions (order: extensions → auth → tables → columns → storage → events)
	diff.Additions = append(diff.Additions, diffNewExtensions(old, new)...)
	diff.Additions = append(diff.Additions, diffNewAuth(old, new)...)
	diff.Additions = append(diff.Additions, diffNewTables(old, new)...)
	diff.Additions = append(diff.Additions, diffNewColumns(old, new)...)
	diff.Additions = append(diff.Additions, diffNewStorage(old, new)...)

	// Alterations (type changes, nullability changes)
	diff.Alterations = append(diff.Alterations, diffColumnChanges(old, new)...)

	return diff
}

// --- Removal functions ---

// diffRemovedTables returns DROP TABLE statements for tables in old but not new.
func diffRemovedTables(old, new *domain.Config) []string {
	var ddl []string
	for name := range old.Tables {
		if _, exists := new.Tables[name]; !exists {
			ddl = append(ddl, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE;", name))
		}
	}
	return ddl
}

// diffRemovedColumns returns DROP COLUMN statements for columns removed from
// tables that still exist.
func diffRemovedColumns(old, new *domain.Config) []string {
	var ddl []string
	for tableName, oldTable := range old.Tables {
		newTable, exists := new.Tables[tableName]
		if !exists {
			continue // table itself is being dropped
		}
		newFieldMap := newTable.FieldMap()
		for _, field := range oldTable.Fields {
			if _, exists := newFieldMap[field.Name]; !exists {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s DROP COLUMN IF EXISTS %s;", tableName, field.Name))
			}
		}
	}
	return ddl
}

// diffRemovedIndexes returns DROP INDEX statements for indexes that existed in
// old but are no longer in new.
func diffRemovedIndexes(old, new *domain.Config) []string {
	var ddl []string
	for tableName, oldTable := range old.Tables {
		newTable, tableExists := new.Tables[tableName]

		for _, idx := range oldTable.Indexes {
			indexName := fmt.Sprintf("idx_%s_%s", tableName, strings.Join(idx.Columns, "_"))
			if !tableExists || !hasIndex(newTable.Indexes, idx) {
				ddl = append(ddl, fmt.Sprintf("DROP INDEX IF EXISTS %s;", indexName))
			}
		}
	}
	return ddl
}

func hasIndex(indexes []domain.Index, target domain.Index) bool {
	for _, idx := range indexes {
		if strings.Join(idx.Columns, ",") == strings.Join(target.Columns, ",") {
			return true
		}
	}
	return false
}

// diffRemovedRLSPolicies returns DROP POLICY statements for policies that
// existed in old but are no longer in new.
func diffRemovedRLSPolicies(old, new *domain.Config) []string {
	var ddl []string
	for tableName, oldTable := range old.Tables {
		newTable, tableExists := new.Tables[tableName]
		if !tableExists {
			continue // table drop cascades policies
		}

		oldPolicies := rlsPolicyNames(tableName, oldTable.RLS)
		newPolicies := rlsPolicyNames(tableName, newTable.RLS)

		for _, name := range oldPolicies {
			if !slices.Contains(newPolicies, name) {
				ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s;", name, tableName))
			}
		}

		// If all RLS policies were removed, disable RLS on the table.
		if len(oldTable.RLS) > 0 && len(newTable.RLS) == 0 {
			ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s DISABLE ROW LEVEL SECURITY;", tableName))
		}
	}
	return ddl
}

func rlsPolicyNames(tableName string, policies []domain.RLSPolicy) []string {
	var names []string
	for i, policy := range policies {
		for _, op := range policy.Operations {
			names = append(names, fmt.Sprintf("%s_%s_%d", tableName, op, i))
		}
	}
	return names
}

// diffRemovedStorageRLS returns DROP POLICY statements for storage policies
// that existed in old but are no longer in new.
func diffRemovedStorageRLS(old, new *domain.Config) []string {
	var ddl []string
	for bucketName, oldBucket := range old.Storage {
		newBucket, bucketExists := new.Storage[bucketName]

		// Drop public select policy if bucket removed or no longer public
		if oldBucket.Public && (!bucketExists || !newBucket.Public) {
			policyName := fmt.Sprintf("%s_public_select", bucketName)
			ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON storage.objects;", policyName))
		}

		oldPolicies := storageRLSPolicyNames(bucketName, oldBucket.RLS)
		var newPolicies []string
		if bucketExists {
			newPolicies = storageRLSPolicyNames(bucketName, newBucket.RLS)
		}

		for _, name := range oldPolicies {
			if !slices.Contains(newPolicies, name) {
				ddl = append(ddl, fmt.Sprintf("DROP POLICY IF EXISTS %s ON storage.objects;", name))
			}
		}
	}
	return ddl
}

func storageRLSPolicyNames(bucketName string, policies []domain.RLSPolicy) []string {
	var names []string
	for i, policy := range policies {
		for _, op := range policy.Operations {
			names = append(names, fmt.Sprintf("storage_%s_%s_%d", bucketName, op, i))
		}
	}
	return names
}

// diffRemovedRPCFunctions returns DROP FUNCTION statements for functions
// that existed in old but are no longer in new.
func diffRemovedRPCFunctions(old, new *domain.Config) []string {
	var ddl []string
	for name, fn := range old.RPC {
		if _, exists := new.RPC[name]; !exists {
			sig := rpcFunctionDropSig(fn)
			ddl = append(ddl, fmt.Sprintf("DROP FUNCTION IF EXISTS public.\"%s\"(%s);", name, sig))
		}
	}
	return ddl
}

// rpcFunctionDropSig builds the argument type list needed for DROP FUNCTION
// to resolve overloaded names.
func rpcFunctionDropSig(fn domain.Function) string {
	types := make([]string, len(fn.Args))
	for i, a := range fn.Args {
		types[i] = a.Type
	}
	return strings.Join(types, ", ")
}

// diffRemovedSearch returns DDL to drop TSVECTOR columns and GIN indexes for
// tables that had searchable config in old but not in new.
func diffRemovedSearch(old, new *domain.Config) []string {
	var ddl []string
	for tableName, oldTable := range old.Tables {
		if len(oldTable.Searchable) == 0 {
			continue
		}
		newTable, exists := new.Tables[tableName]
		if !exists {
			continue // table being dropped, CASCADE handles it
		}
		if len(newTable.Searchable) == 0 {
			ddl = append(ddl, fmt.Sprintf("DROP INDEX IF EXISTS idx_%s_tsv;", tableName))
			ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s DROP COLUMN IF EXISTS _tsv;", tableName))
		}
	}
	return ddl
}

// --- Addition functions ---

// diffNewExtensions returns CREATE EXTENSION statements for extensions in new but not old.
func diffNewExtensions(old, new *domain.Config) []string {
	oldSet := make(map[string]bool)
	for _, ext := range old.Extensions {
		oldSet[ext] = true
	}
	var ddl []string
	for _, ext := range new.Extensions {
		if !oldSet[ext] {
			ddl = append(ddl, fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s;", ext))
		}
	}
	return ddl
}

// diffNewAuth returns DDL for auth table additions. If auth is newly added,
// generates the full auth schema. If auth already existed, handles additive
// schema changes (refresh tokens, email verification).
func diffNewAuth(old, new *domain.Config) []string {
	if new.Auth == nil {
		return nil
	}
	if old.Auth == nil {
		return generateAuthTables(new.Auth)
	}
	var ddl []string
	// Handle transition to refresh tokens
	if new.Auth.RefreshTokens && !old.Auth.RefreshTokens {
		ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.refresh_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  session_id TEXT,
  ip TEXT,
  user_agent TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	}
	// Handle transition to email verification
	if new.Auth.Email != nil && old.Auth.Email == nil {
		ddl = append(ddl, `CREATE TABLE IF NOT EXISTS auth.one_time_tokens (
  id BIGSERIAL PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  purpose TEXT NOT NULL DEFAULT 'signup',
  email TEXT,
  code TEXT,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
		ddl = append(ddl, `CREATE INDEX IF NOT EXISTS idx_one_time_tokens_email_code ON auth.one_time_tokens (email, code);`)
	}
	return ddl
}

// diffNewTables returns CREATE TABLE + CREATE INDEX statements for tables in
// new but not old, in topological (FK-dependency) order.
func diffNewTables(old, new *domain.Config) []string {
	var ddl []string
	ordered := orderTables(new.Tables)
	for _, name := range ordered {
		if _, exists := old.Tables[name]; !exists {
			table := new.Tables[name]
			ddl = append(ddl, generateTable(name, table, new.Tables)...)
			ddl = append(ddl, generateIndexes(name, table)...)
		}
	}
	return ddl
}

// diffNewColumns returns ALTER TABLE ADD COLUMN statements for fields present
// in new tables but absent from old tables (only for tables that exist in both).
func diffNewColumns(old, new *domain.Config) []string {
	var ddl []string
	tableNames := sortedKeys(new.Tables)
	for _, tableName := range tableNames {
		newTable := new.Tables[tableName]
		oldTable, exists := old.Tables[tableName]
		if !exists {
			continue // new table handled by diffNewTables
		}
		oldFieldMap := oldTable.FieldMap()
		for _, field := range newTable.Fields {
			fieldName := field.Name
			if _, exists := oldFieldMap[fieldName]; exists {
				continue // existing field, handled by diffColumnChanges
			}
			colDef := formatColumnForAdd(fieldName, field)
			ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", tableName, colDef))

			// Constraints for the new column
			if len(field.Enum) > 0 {
				quoted := make([]string, len(field.Enum))
				for i, v := range field.Enum {
					quoted[i] = "'" + v + "'"
				}
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD CHECK (%s IN (%s));",
					tableName, fieldName, strings.Join(quoted, ", ")))
			}
			if field.Pattern != "" {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD CHECK (%s ~ '%s');",
					tableName, fieldName, field.Pattern))
			}
			if field.Min != nil {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD CHECK (%s >= %g);",
					tableName, fieldName, *field.Min))
			}
			if field.Max != nil {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD CHECK (%s <= %g);",
					tableName, fieldName, *field.Max))
			}
			if field.Check != "" {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD CHECK (%s);",
					tableName, field.Check))
			}
			if field.Unique {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ADD UNIQUE (%s);",
					tableName, fieldName))
			}
		}
	}
	return ddl
}

// formatColumnForAdd builds a column definition for ALTER TABLE ADD COLUMN,
// including inline REFERENCES for FK columns.
func formatColumnForAdd(name string, field domain.Field) string {
	def := formatColumn(name, field)
	if field.ForeignKey != nil {
		schema, refTable, refCol, err := domain.ParseFKReference(field.ForeignKey.References)
		if err == nil {
			onDelete := "RESTRICT"
			if field.ForeignKey.OnDelete != "" {
				onDelete = strings.ToUpper(strings.ReplaceAll(field.ForeignKey.OnDelete, "_", " "))
			}
			def += fmt.Sprintf(" REFERENCES %s.%s(%s) ON DELETE %s", schema, refTable, refCol, onDelete)
		}
		// On parse error: omit the REFERENCES clause. Validation runs upstream
		// and rejects malformed references; if it didn't, we'd rather emit a
		// column without a constraint than malformed SQL.
	}
	return def
}

// diffNewStorage returns DDL to create the storage.objects table when storage
// is newly added (present in new but not old).
func diffNewStorage(old, new *domain.Config) []string {
	if len(new.Storage) == 0 {
		return nil
	}
	if len(old.Storage) == 0 {
		return generateStorageTables(new)
	}
	return nil
}

// --- Alteration functions ---

// diffColumnChanges returns ALTER TABLE statements for type changes and
// nullability changes on columns that exist in both old and new configs.
func diffColumnChanges(old, new *domain.Config) []string {
	var ddl []string
	tableNames := sortedKeys(new.Tables)
	for _, tableName := range tableNames {
		newTable := new.Tables[tableName]
		oldTable, exists := old.Tables[tableName]
		if !exists {
			continue // new table, handled elsewhere
		}
		oldFieldMap := oldTable.FieldMap()
		for _, newField := range newTable.Fields {
			fieldName := newField.Name
			oldField, exists := oldFieldMap[fieldName]
			if !exists {
				continue // new column, handled by diffNewColumns
			}

			newType := normalizeType(effectiveType(newField))
			oldType := normalizeType(effectiveType(oldField))
			if newType != oldType {
				ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;",
					tableName, fieldName, effectiveType(newField)))
			}

			newRequired := newField.Required || newField.PrimaryKey
			oldRequired := oldField.Required || oldField.PrimaryKey
			if newRequired != oldRequired {
				if newRequired {
					ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;",
						tableName, fieldName))
				} else {
					ddl = append(ddl, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;",
						tableName, fieldName))
				}
			}
		}
	}
	return ddl
}

// normalizeType maps config types to a comparable form so that equivalent
// types (e.g. bigserial → bigint) don't trigger spurious ALTER statements.
func normalizeType(t string) string {
	t = strings.ToLower(t)
	switch {
	case t == "bigserial":
		return "bigint"
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

// --- Helpers ---

// Test-only shims: these lowercase wrappers allow existing tests written
// against the old unexported names to continue to compile after the pure
// SQL-building functions were moved into the postgrest sub-package.
// They are compiled only when running tests (this is a _test.go file).
package http

import (
	"github.com/instancez/instancez/internal/adapter/http/postgrest"
	"github.com/instancez/instancez/internal/domain"
)

func unionColumns(rows []map[string]any) []string {
	return postgrest.UnionColumns(rows)
}

func buildBulkInsertQuery(tableName string, records []map[string]any, returning bool) (string, []any) {
	return postgrest.BuildBulkInsertQuery(tableName, records, returning)
}

func buildBulkUpsertQuery(tableName string, records []map[string]any, conflictCols []string, resolution string, returning bool) (string, []any) {
	return postgrest.BuildBulkUpsertQuery(tableName, records, conflictCols, resolution, returning)
}

func renderRowTuples(records []map[string]any, cols []string, startArg int) ([]any, []string) {
	return postgrest.RenderRowTuples(records, cols, startArg)
}

func parseColumnsParam(val string, table domain.Table) (map[string]bool, error) {
	return postgrest.ParseColumnsParam(val, table)
}

func filterRecordsByColumns(records []map[string]any, allowed map[string]bool) []map[string]any {
	return postgrest.FilterRecordsByColumns(records, allowed)
}

func buildSelectQuery(tableName string, qp *postgrest.QueryParams, table domain.Table) (string, []any) {
	return postgrest.BuildSelectQuery(tableName, qp, table)
}

func buildSelectQueryFull(tableName string, qp *postgrest.QueryParams, table domain.Table, allTables map[string]domain.Table) (string, []any) {
	return postgrest.BuildSelectQueryFull(tableName, qp, table, allTables)
}

func parseOrderValue(val string, table domain.Table) ([]postgrest.OrderClause, error) {
	return postgrest.ParseOrderValue(val, table)
}

func parseOrderValueWithSelect(val string, table domain.Table, selectItems []string) ([]postgrest.OrderClause, error) {
	return postgrest.ParseOrderValueWithSelect(val, table, selectItems)
}

func renderOrderBy(clauses []postgrest.OrderClause) string {
	return postgrest.RenderOrderBy(clauses)
}

func recordsAllEmpty(records []map[string]any) bool {
	return postgrest.RecordsAllEmpty(records)
}

func validateColumn(table domain.Table, col string) error {
	return postgrest.ValidateColumn(table, col)
}

func parseSelectParam(sel string) []string {
	return postgrest.ParseSelectParam(sel)
}

func buildInsertQuery(tableName string, record map[string]any, returning bool) (string, []any) {
	return postgrest.BuildInsertQuery(tableName, record, returning)
}

func buildUpdateQuery(tableName string, updates map[string]any, where *postgrest.WhereNode, returning bool) (string, []any) {
	return postgrest.BuildUpdateQuery(tableName, updates, where, returning)
}

func buildDeleteQuery(tableName string, where *postgrest.WhereNode, returning bool) (string, []any) {
	return postgrest.BuildDeleteQuery(tableName, where, returning)
}

func findUnknownFields(record map[string]any, fieldMap map[string]domain.Field) []string {
	return postgrest.FindUnknownFields(record, fieldMap)
}

func parseEmbedParam(s string) (name, alias string, cols []string, nested []string, spread bool) {
	return postgrest.ParseEmbedParam(s)
}

func resolveEmbeds(tableName string, table domain.Table, embedNames []string, allTables map[string]domain.Table) ([]postgrest.Embed, error) {
	return postgrest.ResolveEmbeds(tableName, table, embedNames, allTables)
}

func parseOnConflictParam(val string, table domain.Table) ([]string, error) {
	return postgrest.ParseOnConflictParam(val, table)
}

func primaryKeyColumns(table domain.Table) []string {
	return postgrest.PrimaryKeyColumns(table)
}

func buildUpsertQuery(tableName string, record map[string]any, conflictCols []string, resolution string, returning bool) (string, []any) {
	return postgrest.BuildUpsertQuery(tableName, record, conflictCols, resolution, returning)
}

func aliasWhereColumns(n *postgrest.WhereNode, alias string) *postgrest.WhereNode {
	return postgrest.AliasWhereColumns(n, alias)
}

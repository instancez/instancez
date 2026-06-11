# Codebase Quality Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve code quality across five dimensions: CRUD mutation deduplication, PostgREST query engine package split, golangci-lint CI, storage + dashboard test coverage, and auth service extraction.

**Architecture:** Hexagonal layout — `internal/domain` (ports), `internal/adapter/*` (implementations), `internal/app` (orchestration). Each phase ends with a green test suite before the next phase starts.

**Tech Stack:** Go 1.25, Gin, pgx/v5, golangci-lint, vitest, React/Vite, testing-library

---

## Phase 1 — CRUD mutation handler deduplication

### Task 1: Extract parseRequestBody helper

**Files:**
- Modify: `internal/adapter/http/crud_handler.go`

- [ ] **Step 1: Add `parseRequestBody` after the last helper function in crud_handler.go (anywhere before the closing brace of the file)**

```go
// parseRequestBody parses POST/PUT body as records (CSV, JSON array, or single JSON object).
// On failure it writes a problemJSON response and returns nil, err. Callers must return on non-nil err.
func parseRequestBody(c *gin.Context, table domain.Table) ([]map[string]any, error) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		problemJSON(c, 400, "bad_request", "Cannot read request body")
		return nil, err
	}
	var records []map[string]any
	if contentTypeIsCSV(c.GetHeader("Content-Type")) {
		records, err = csvReadRecords(body)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return nil, err
		}
		records = csvCoerceRecords(records, table)
	} else {
		trimmed := strings.TrimSpace(string(body))
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal(body, &records); err != nil {
				problemJSON(c, 400, "bad_request", "Invalid JSON array")
				return nil, err
			}
		} else {
			var single map[string]any
			if err := json.Unmarshal(body, &single); err != nil {
				problemJSON(c, 400, "bad_request", "Invalid JSON")
				return nil, err
			}
			records = []map[string]any{single}
		}
	}
	if len(records) == 0 {
		problemJSON(c, 400, "bad_request", "Empty request body")
		return nil, fmt.Errorf("empty body")
	}
	return records, nil
}
```

- [ ] **Step 2: Run tests**

```sh
cd /path/to/repo && go test -race ./internal/adapter/http/...
```
Expected: PASS (function added but not used yet)

- [ ] **Step 3: Commit**

```sh
git add internal/adapter/http/crud_handler.go
git commit -m "refactor(http): add parseRequestBody helper"
```

---

### Task 2: Extract setupMutationTx + writeMutationResponse; refactor the three handlers

**Files:**
- Modify: `internal/adapter/http/crud_handler.go`

- [ ] **Step 1: Add `setupMutationTx` and `writeMutationResponse` at the bottom of crud_handler.go**

```go
// setupMutationTx creates an RLS context and begins a transaction.
// On failure it writes a problemJSON to c and returns non-nil error. Callers must return immediately.
func setupMutationTx(c *gin.Context, db domain.Database, session domain.Session) (context.Context, domain.Tx, func(context.Context), error) {
	ctx, err := db.WithRLS(c.Request.Context(), session)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to set RLS context")
		return nil, nil, nil, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to start transaction")
		return nil, nil, nil, err
	}
	return ctx, tx, func(ctx context.Context) { tx.Rollback(ctx) }, nil
}

// writeMutationResponse writes the HTTP response for a mutation.
// status is 201 for create, 200 for upsert/update.
func writeMutationResponse(c *gin.Context, status int, returnMode string, results []map[string]any) {
	switch returnMode {
	case "representation":
		if c.GetHeader("Accept") == "application/vnd.pgrst.object+json" && len(results) == 1 {
			c.JSON(status, results[0])
		} else {
			if results == nil {
				results = []map[string]any{}
			}
			c.JSON(status, results)
		}
	case "headers-only":
		c.Header("Preference-Applied", "return=headers-only")
		c.Status(status)
	default:
		c.Status(status)
	}
}
```

- [ ] **Step 2: Replace the body-parse + tx-setup block in `handleCreate` (lines ~330–402) with calls to helpers**

The new `handleCreate` body (inside the returned `func(c *gin.Context)`) should be:

```go
session := getSession(c)
prefer := joinPrefer(c)
if !enforceStrictPrefer(c, prefer) {
    return
}

records, err := parseRequestBody(c, table)
if err != nil {
    return
}

allowedCols, err := parseColumnsParam(c.Query("columns"), table)
if err != nil {
    problemJSON(c, 400, "bad_request", err.Error())
    return
}
records = filterRecordsByColumns(records, allowedCols)
if recordsAllEmpty(records) {
    problemJSON(c, 400, "bad_request", "No insertable columns after applying columns= filter")
    return
}

fieldMap := table.FieldMap()
for _, rec := range records {
    if unknowns := findUnknownFields(rec, fieldMap); len(unknowns) > 0 {
        problemJSON(c, 400, "bad_request",
            fmt.Sprintf("Unknown fields: %s", strings.Join(unknowns, ", ")))
        return
    }
}

ctx, tx, rollback, err := setupMutationTx(c, h.db, session)
if err != nil {
    return
}
defer rollback(ctx)

returnMode := parseReturnPrefer(prefer)
resolution := parseResolutionPrefer(prefer)
var results []map[string]any

var pkCols []string
if resolution != "" {
    customCols, err := parseOnConflictParam(c.Query("on_conflict"), table)
    if err != nil {
        problemJSON(c, 400, "bad_request", err.Error())
        return
    }
    if len(customCols) > 0 {
        pkCols = customCols
    } else {
        pkCols = primaryKeyColumns(table)
    }
    if len(pkCols) == 0 {
        problemJSON(c, 400, "bad_request", "Cannot upsert: table has no primary key and no on_conflict")
        return
    }
}

var query string
var args []any
if resolution != "" {
    query, args = buildBulkUpsertQuery(tableName, records, pkCols, resolution, returnMode == "representation")
} else {
    query, args = buildBulkInsertQuery(tableName, records, returnMode == "representation")
}

if returnMode == "representation" {
    rows, err := tx.Query(ctx, query, args...)
    if err != nil {
        handleDBError(c, err)
        return
    }
    results = rows
} else {
    if _, err := tx.Exec(ctx, query, args...); err != nil {
        handleDBError(c, err)
        return
    }
}

if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
    return
}
if parseMissingPrefer(prefer) {
    c.Header("Preference-Applied", "missing=default")
}
writeMutationResponse(c, 201, returnMode, results)
```

- [ ] **Step 3: Replace the body-parse + tx-setup block in `handleUpsert` (lines ~490–561) with helpers**

```go
session := getSession(c)
prefer := joinPrefer(c)
if !enforceStrictPrefer(c, prefer) {
    return
}

records, err := parseRequestBody(c, table)
if err != nil {
    return
}

pkCols := primaryKeyColumns(table)
if len(pkCols) == 0 {
    problemJSON(c, 400, "bad_request", "Cannot upsert: table has no primary key")
    return
}

upsertFieldMap := table.FieldMap()
for _, rec := range records {
    if unknowns := findUnknownFields(rec, upsertFieldMap); len(unknowns) > 0 {
        problemJSON(c, 400, "bad_request",
            fmt.Sprintf("Unknown fields: %s", strings.Join(unknowns, ", ")))
        return
    }
    for _, pk := range pkCols {
        if _, ok := rec[pk]; !ok {
            problemJSON(c, 400, "bad_request",
                fmt.Sprintf("PUT body missing primary key column %q", pk))
            return
        }
    }
}

ctx, tx, rollback, err := setupMutationTx(c, h.db, session)
if err != nil {
    return
}
defer rollback(ctx)

returnMode := parseReturnPrefer(prefer)
var results []map[string]any

query, args := buildBulkUpsertQuery(tableName, records, pkCols, "merge", returnMode == "representation")
if returnMode == "representation" {
    rows, err := tx.Query(ctx, query, args...)
    if err != nil {
        handleDBError(c, err)
        return
    }
    results = rows
} else {
    if _, err := tx.Exec(ctx, query, args...); err != nil {
        handleDBError(c, err)
        return
    }
}

if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
    return
}
if parseMissingPrefer(prefer) {
    c.Header("Preference-Applied", "missing=default")
}
writeMutationResponse(c, 200, returnMode, results)
```

- [ ] **Step 4: Replace the tx-setup block in `handleUpdate` (lines ~641–655) with `setupMutationTx`**

Find and replace:
```go
// OLD:
ctx, err := h.db.WithRLS(c.Request.Context(), session)
if err != nil {
    problemJSON(c, 500, "internal", "Failed to set RLS context")
    return
}

returnMode := parseReturnPrefer(prefer)
maxAffected, hasMax := parseMaxAffectedPrefer(prefer)

tx, err := h.db.Begin(ctx)
if err != nil {
    problemJSON(c, 500, "internal", "Failed to start transaction")
    return
}
defer tx.Rollback(ctx)
```

```go
// NEW:
ctx, tx, rollback, err := setupMutationTx(c, h.db, session)
if err != nil {
    return
}
defer rollback(ctx)

returnMode := parseReturnPrefer(prefer)
maxAffected, hasMax := parseMaxAffectedPrefer(prefer)
```

- [ ] **Step 5: Run tests**

```sh
go test -race ./internal/adapter/http/...
```
Expected: PASS

- [ ] **Step 6: Commit Phase 1**

```sh
git add internal/adapter/http/crud_handler.go
git commit -m "refactor(http): deduplicate CRUD mutation handler scaffolding"
```

---

## Phase 2 — PostgREST query engine subpackage

### Task 3: Create postgrest/ package with types

**Files:**
- Create: `internal/adapter/http/postgrest/types.go`

- [ ] **Step 1: Create the directory and types file**

```go
// Package postgrest implements the PostgREST-compatible query engine.
// All functions in this package are pure (no *gin.Context dependency).
package postgrest

import "github.com/instancez/instancez/internal/domain"

// Embed represents a relation embed in a select parameter.
type Embed struct {
	Name      string
	Alias     string
	Columns   []string
	FKColumn  string
	RefTable  string
	RefColumn string
	IsReverse bool
	Inner     bool
	Spread    bool
	Children  []Embed

	Where  *WhereNode
	Order  []OrderClause
	Limit  *int
	Offset *int
}

// OutputKey returns the JSON key for this embed in responses.
func (e Embed) OutputKey() string {
	if e.Alias != "" {
		return e.Alias
	}
	return e.Name
}

// QueryParams holds parsed PostgREST query parameters.
type QueryParams struct {
	Select []string
	Embeds []Embed
	Where  *WhereNode
	Having *WhereNode
	Order  []OrderClause
	Limit  int
	Offset int
}

// Filter is a column comparison in a WHERE clause.
type Filter struct {
	Column   string
	Operator string
	Value    string
	Config   string
}

// OrderClause is a single ORDER BY term.
type OrderClause struct {
	Column  string
	Desc    bool
	Nulls   string
	IsAlias bool
}

// jsonPathStep is one link in a JSONB accessor chain.
type jsonPathStep struct {
	op    string
	key   string
	isInt bool
}

// ColValidator validates a column name. Returns nil to accept, error to reject.
type ColValidator func(col string) error

// ColumnValidator builds a ColValidator that checks against the table's fields.
func ColumnValidator(table domain.Table) ColValidator {
	return func(col string) error {
		return ValidateColumn(table, col)
	}
}
```

- [ ] **Step 2: Verify the file is syntactically correct**

```sh
gofmt -l internal/adapter/http/postgrest/types.go
```
Expected: no output (file is already formatted)

---

### Task 4: Move where.go to postgrest/where.go with gin decoupling

**Files:**
- Create: `internal/adapter/http/postgrest/where.go`
- Delete: `internal/adapter/http/where.go`
- Modify: `internal/adapter/http/crud_handler.go` (call sites at lines ~635, ~706, ~1288, ~1298)
- Modify: `internal/adapter/http/rpc_handler.go` (call site at line ~474)
- Modify: `internal/adapter/http/validation_test.go` (call sites)

- [ ] **Step 1: Create `internal/adapter/http/postgrest/where.go`**

Copy all contents of `where.go`, change to `package postgrest`. Remove the `WhereNode`, `Filter`, `colValidator` type declarations (now in `types.go`). Add `"net/url"` to imports. Change the three gin-coupled signatures:

```go
// ParseWhere builds a filter tree from url.Values query parameters.
func ParseWhere(vals url.Values, tableName string, table domain.Table) (*WhereNode, error) {
	return ParseWhereSkip(vals, tableName, table, nil)
}

// ParseWhereSkip is ParseWhere but skips keys whose prefix matches any in skipPrefixes.
func ParseWhereSkip(vals url.Values, tableName string, table domain.Table, skipPrefixes map[string]bool) (*WhereNode, error) {
	validate := ColumnValidator(table)
	tree, err := ParseWhereWith(vals, validate, skipPrefixes, nil)
	if err != nil {
		return nil, err
	}
	if err := validateFTSLeaves(tree, table); err != nil {
		return nil, err
	}
	return tree, nil
}

// ParseWhereWith is the lowest-level filter parser. validate is called on every column ref.
func ParseWhereWith(vals url.Values, validate ColValidator, skipPrefixes map[string]bool, skipKeys map[string]bool) (*WhereNode, error) {
	root := &WhereNode{Op: "and"}

	for key, values := range vals {   // ← vals instead of c.Request.URL.Query()
		// ... rest of existing logic unchanged ...
	}

	if len(root.Children) == 0 {
		return nil, nil
	}
	return root, nil
}
```

All other functions in `where.go` (`andLeaves`, `buildSQL`, helper funcs like `parseLeafValue`, `parseLogicListWith`, `validateFTSLeaves`, etc.) move verbatim — just change the package name and rename `colValidator` → `ColValidator` where it appears as a parameter type.

Export `AndLeaves` (was `andLeaves`). Keep `buildSQL` as a method on `*WhereNode` (unchanged).

- [ ] **Step 2: Delete the old `where.go`**

```sh
rm internal/adapter/http/where.go
```

- [ ] **Step 3: Update call sites in `crud_handler.go`**

Find the four call sites and update:
```go
// At handleUpdate (~line 635):
// OLD: where, err := parseWhere(c, tableName, table)
// NEW:
where, err := postgrest.ParseWhere(c.Request.URL.Query(), tableName, table)

// At handleDelete (~line 706):
// OLD: where, err := parseWhere(c, tableName, table)
// NEW:
where, err := postgrest.ParseWhere(c.Request.URL.Query(), tableName, table)

// At parseQueryParams (~line 1288):
// OLD: if err := parseEmbedScopedParams(c, embedByName, allTables); err != nil {
// NEW: (covered in Task 5 — parseEmbedScopedParams moves to postgrest)

// At parseQueryParams (~line 1298):
// OLD: where, err := parseWhereSkip(c, tableName, table, skipPrefixes)
// NEW:
where, err = postgrest.ParseWhereSkip(c.Request.URL.Query(), tableName, table, skipPrefixes)
```

Add `"github.com/instancez/instancez/internal/adapter/http/postgrest"` to crud_handler.go imports.

- [ ] **Step 4: Update call site in `rpc_handler.go` (~line 474)**

```go
// OLD: tree, err := parseWhereWith(c, validate, nil, argNames)
// NEW:
tree, err := postgrest.ParseWhereWith(c.Request.URL.Query(), validate, nil, argNames)
```

`validate` here is a `colValidator` function — rename its type at the declaration site to `postgrest.ColValidator`.

- [ ] **Step 5: Update `validation_test.go` call sites**

```go
// OLD: where, err := parseWhere(c, "todos", table)
// NEW:
where, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table)
```

`parseQueryParams` call stays unchanged (it remains in the `http` package).

- [ ] **Step 6: Run tests**

```sh
go test -race ./internal/adapter/http/...
```
Expected: PASS

---

### Task 5: Move parseEmbedScopedParams to postgrest/; move select.go and csv.go

**Files:**
- Modify: `internal/adapter/http/crud_handler.go` (remove `parseEmbedScopedParams`, update call site)
- Create: `internal/adapter/http/postgrest/params.go` (holds `ParseEmbedScopedParams`)
- Create: `internal/adapter/http/postgrest/select.go`
- Create: `internal/adapter/http/postgrest/csv.go`
- Delete: `internal/adapter/http/select.go`
- Delete: `internal/adapter/http/csv.go`

- [ ] **Step 1: Move `parseEmbedScopedParams` from crud_handler.go to postgrest/params.go**

Create `internal/adapter/http/postgrest/params.go`:

```go
package postgrest

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// ParseEmbedScopedParams routes "<embed>.*" query parameters into the
// corresponding Embed's Where/Order/Limit fields.
func ParseEmbedScopedParams(vals url.Values, embedByName map[string]*Embed, allTables map[string]domain.Table) error {
	for key, values := range vals {
		for embName, emb := range embedByName {
			prefix := embName + "."
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			suffix := strings.TrimPrefix(key, prefix)
			embTable, ok := allTables[emb.RefTable]
			if !ok {
				return fmt.Errorf("embed %q references unknown table %q", embName, emb.RefTable)
			}
			aliasSet := CollectSelectAliases(emb.Columns)
			embValidate := func(col string) error {
				if _, ok := aliasSet[col]; ok {
					return nil
				}
				return ValidateColumn(embTable, col)
			}
			switch suffix {
			case "or", "and":
				for _, v := range values {
					node, err := parseLogicListWith(suffix, v, embValidate)
					if err != nil {
						return fmt.Errorf("invalid %s for embed %q: %w", suffix, embName, err)
					}
					if emb.Where == nil {
						emb.Where = &WhereNode{Op: "and"}
					}
					emb.Where.Children = append(emb.Where.Children, node)
				}
			case "order":
				if !emb.IsReverse {
					return fmt.Errorf("order not allowed on belongs-to embed %q", embName)
				}
				for _, v := range values {
					clauses, err := ParseOrderValueWithSelect(v, embTable, emb.Columns)
					if err != nil {
						return fmt.Errorf("invalid order for embed %q: %w", embName, err)
					}
					emb.Order = append(emb.Order, clauses...)
				}
			case "limit":
				if !emb.IsReverse {
					return fmt.Errorf("limit not allowed on belongs-to embed %q", embName)
				}
				if len(values) == 0 {
					break
				}
				n, err := strconv.Atoi(values[0])
				if err != nil || n < 0 {
					return fmt.Errorf("invalid limit for embed %q: %s", embName, values[0])
				}
				emb.Limit = &n
			default:
				for _, v := range values {
					leaf, err := parseLeafValue(suffix, v)
					if err != nil {
						return fmt.Errorf("invalid filter on embed %q field %q: %w", embName, suffix, err)
					}
					if emb.Where == nil {
						emb.Where = &WhereNode{Op: "and"}
					}
					emb.Where.Children = append(emb.Where.Children, leaf)
				}
			}
		}
	}
	return nil
}
```

Delete `parseEmbedScopedParams` from `crud_handler.go` and update its call site (~line 1288):
```go
// OLD: if err := parseEmbedScopedParams(c, embedByName, allTables); err != nil {
// NEW:
if err := postgrest.ParseEmbedScopedParams(c.Request.URL.Query(), embedByName, allTables); err != nil {
```

- [ ] **Step 2: Copy `select.go` to `postgrest/select.go`**

Copy the file, change `package http` → `package postgrest`. Export all functions:
- `parseSelectItem` → `ParseSelectItem`
- `validateSelectItem` → `ValidateSelectItem`
- `renderSelectItem` → `RenderSelectItem`

`SelectItem` struct is already exported — keep the name.

Update any internal calls within the file to use the new names.

- [ ] **Step 3: Copy `csv.go` to `postgrest/csv.go`**

Copy the file, change `package http` → `package postgrest`. Export:
- `csvReadRecords` → `CsvReadRecords`
- `csvCoerceRecords` → `CsvCoerceRecords`

- [ ] **Step 4: Delete the old files**

```sh
rm internal/adapter/http/select.go internal/adapter/http/csv.go
```

- [ ] **Step 5: Update all call sites in the `http` package**

In `crud_handler.go` and `parseRequestBody`:
```go
csvReadRecords(body)    → postgrest.CsvReadRecords(body)
csvCoerceRecords(...)   → postgrest.CsvCoerceRecords(...)
parseSelectItem(s)      → postgrest.ParseSelectItem(s)
validateSelectItem(...) → postgrest.ValidateSelectItem(...)
renderSelectItem(...)   → postgrest.RenderSelectItem(...)
```

- [ ] **Step 6: Run tests**

```sh
go test -race ./internal/adapter/http/...
```
Expected: PASS

---

### Task 6: Move all remaining pure builder functions from crud_handler.go to postgrest/builders.go

**Files:**
- Create: `internal/adapter/http/postgrest/builders.go`
- Modify: `internal/adapter/http/crud_handler.go`

- [ ] **Step 1: Identify all functions to move**

These are the functions in `crud_handler.go` that have no `*gin.Context` parameter. Find them with:
```sh
grep -n "^func " internal/adapter/http/crud_handler.go | grep -v "func (h \*CRUDHandler)"
```

The list includes (check the actual file — these are the expected ones):
- `buildSelectQueryFull`, `buildSelectQuery`
- `buildFilterCondition`
- `buildBulkInsertQuery`, `buildBulkUpsertQuery`, `buildInsertQuery`, `buildUpdateQuery`, `buildDeleteQuery`
- `renderOrderBy`, `renderJSONBSuffix`, `lastJSONBOp`
- `resolveEmbeds`, `buildEmbedRowExpr`, `buildChildEmbedSubselect`
- `parseSelectParam`, `parseOrderValue`, `parseOrderValueWithSelect`, `parseOrderValueWith`, `collectSelectAliases`
- `parseEmbedParam`
- `parseFilterValue`, `parsePatternList`
- `splitJSONBPath`, `isAllDigits`
- `qualifyOrderColumns`, `aliasWhereColumns`
- `hasBelongsToJoin`
- `recordsAllEmpty`, `unionColumns`, `renderRowTuples`
- `filterRecordsByColumns`, `parseColumnsParam`
- `primaryKeyColumns`
- `validateColumn`, `findUnknownFields`
- `parseOnConflictParam`, `isAggSelectEntry`
- `parseHavingParam` (if it exists without gin.Context)

Also move the `Embed`, `QueryParams`, `Filter`, `OrderClause`, `jsonPathStep` type declarations that are still in `crud_handler.go` (they now live in `postgrest/types.go`).

- [ ] **Step 2: Create `internal/adapter/http/postgrest/builders.go`**

```go
package postgrest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)
```

Then paste all the moved functions verbatim, exporting their names (capitalize first letter).

Key exports needed by `crud_handler.go`:
- `BuildBulkInsertQuery`, `BuildBulkUpsertQuery`, `BuildUpdateQuery`, `BuildDeleteQuery`
- `BuildSelectQueryFull`, `BuildSelectQuery`
- `ResolveEmbeds`
- `ParseSelectParam`, `ParseOrderValueWithSelect`
- `FilterRecordsByColumns`, `ParseColumnsParam`, `RecordsAllEmpty`
- `PrimaryKeyColumns`, `FindUnknownFields`, `ValidateColumn`
- `ParseOnConflictParam`

- [ ] **Step 3: Remove the moved declarations from crud_handler.go**

Delete all the function bodies that were moved and the type declarations (`Embed`, `QueryParams`, `Filter`, `OrderClause`, `jsonPathStep`) since they now live in `postgrest/types.go`.

Replace every internal call in crud_handler.go to use `postgrest.FunctionName(...)`.

Example:
```go
// OLD:
query, args = buildBulkInsertQuery(tableName, records, returnMode == "representation")
// NEW:
query, args = postgrest.BuildBulkInsertQuery(tableName, records, returnMode == "representation")

// OLD:
fieldMap := table.FieldMap()
if unknowns := findUnknownFields(rec, fieldMap); ...
// NEW:
if unknowns := postgrest.FindUnknownFields(rec, table.FieldMap()); ...

// OLD:
pkCols = primaryKeyColumns(table)
// NEW:
pkCols = postgrest.PrimaryKeyColumns(table)
```

- [ ] **Step 4: Update `parseQueryParams` in crud_handler.go**

`parseQueryParams` stays in the `http` package (gin-coupled orchestrator). Update its internal calls:
```go
// select param parsing:
qp.Select = postgrest.ParseSelectParam(sel)
// order:
clauses, err := postgrest.ParseOrderValueWithSelect(order, table, qp.Select)
// embeds:
resolved, err := postgrest.ResolveEmbeds(tableName, table, embedParams, allTables)
// columns= for columns hint:
allowedCols, err := postgrest.ParseColumnsParam(...)
```

Also update `QueryParams` references — it's now `postgrest.QueryParams`. The function returns `*postgrest.QueryParams`:
```go
func parseQueryParams(c *gin.Context, tableName string, table domain.Table, allTables map[string]domain.Table) (*postgrest.QueryParams, error) {
```

- [ ] **Step 5: Run tests**

```sh
go test -race ./internal/adapter/http/...
```
Expected: PASS

---

### Task 7: Run full test suite — Phase 2 green gate; commit

**Files:**
- Possibly move: `internal/adapter/http/where_test.go` → `internal/adapter/http/postgrest/where_test.go`

- [ ] **Step 1: Check for any test files testing moved functions**

```sh
ls internal/adapter/http/*_test.go
grep -l "parseWhere\|parseWhereWith\|buildBulkInsert\|buildSelect" internal/adapter/http/*_test.go
```

For each test file that tests functions now in the `postgrest` package, move the file to `internal/adapter/http/postgrest/` and change `package http` to `package postgrest_test`. Update function calls to use exported names.

- [ ] **Step 2: Run the full suite**

```sh
go test -race ./...
```
Expected: PASS — Phase 2 green gate.

- [ ] **Step 3: Commit Phase 2**

```sh
git add internal/adapter/http/postgrest/ internal/adapter/http/
git commit -m "refactor(http): extract PostgREST query engine into internal/adapter/http/postgrest"
```

---

## Phase 3 — golangci-lint

### Task 8: Add .golangci.yml and lint CI job

**Files:**
- Create: `.golangci.yml`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Create `.golangci.yml` at the repo root**

```yaml
linters:
  enable:
    - errcheck
    - staticcheck
    - gosimple
    - unused
    - govet
    - ineffassign
    - typecheck
  disable:
    - wrapcheck
    - exhaustive
    - exhaustruct

linters-settings:
  errcheck:
    check-blank: true

issues:
  exclude-rules:
    # Fire-and-forget DB updates (e.g. last_sign_in_at) — intentional
    - linters: [errcheck]
      text: "h\\.db\\.Exec"
      path: "auth_handler\\.go"
```

- [ ] **Step 2: Run golangci-lint locally to see the baseline**

```sh
golangci-lint run ./... 2>&1 | head -80
```

- [ ] **Step 3: Fix any errcheck, unused, or staticcheck failures found**

For each unfixed `errcheck` finding (outside the excluded auth_handler.go pattern), wrap the error:
```go
// Example:
if _, err := someFunc(); err != nil {
    h.logger.Error("operation failed", "error", err)
}
```

For `unused` or `ineffassign` findings, either remove unused code or fix the assignment.

- [ ] **Step 4: Confirm lint is clean**

```sh
golangci-lint run ./...
```
Expected: exit 0

- [ ] **Step 5: Add lint job to `.github/workflows/ci.yml`**

After the existing `dashboard` job, add:
```yaml
  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
```

The `lint` job does not need to gate the Docker build job — non-blocking for first introduction.

- [ ] **Step 6: Commit Phase 3**

```sh
git add .golangci.yml .github/workflows/ci.yml
git commit -m "ci: add golangci-lint step"
```

---

## Phase 4 — New tests

### Task 9: Add storage_v1_handler_test.go (bucket handlers)

**Files:**
- Create: `internal/adapter/http/storage_v1_handler_test.go`

- [ ] **Step 1: Create the test file with stubObjectStore and helpers**

```go
package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

// stubObjectStore implements domain.ObjectStore for unit tests.
type stubObjectStore struct {
	uploadFn       func(ctx context.Context, key string, r io.Reader, ct string, size int64) error
	downloadFn     func(ctx context.Context, key string) (io.ReadCloser, string, error)
	deleteFn       func(ctx context.Context, key string) error
	headFn         func(ctx context.Context, key string) (domain.ObjectInfo, error)
	listFn         func(ctx context.Context, prefix string) ([]domain.ObjectInfo, error)
	signUploadFn   func(ctx context.Context, key, ct string, expiry time.Duration) (string, error)
	signDownloadFn func(ctx context.Context, key string, expiry time.Duration) (string, error)
	copyFn         func(ctx context.Context, src, dst string) error
	ensureBucketFn func(ctx context.Context, bucket string) error
}

func (s *stubObjectStore) Upload(ctx context.Context, key string, r io.Reader, ct string, size int64) error {
	if s.uploadFn != nil {
		return s.uploadFn(ctx, key, r, ct, size)
	}
	return nil
}
func (s *stubObjectStore) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if s.downloadFn != nil {
		return s.downloadFn(ctx, key)
	}
	return io.NopCloser(strings.NewReader("")), "application/octet-stream", nil
}
func (s *stubObjectStore) Delete(ctx context.Context, key string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, key)
	}
	return nil
}
func (s *stubObjectStore) Head(ctx context.Context, key string) (domain.ObjectInfo, error) {
	if s.headFn != nil {
		return s.headFn(ctx, key)
	}
	return domain.ObjectInfo{}, nil
}
func (s *stubObjectStore) List(ctx context.Context, prefix string) ([]domain.ObjectInfo, error) {
	if s.listFn != nil {
		return s.listFn(ctx, prefix)
	}
	return nil, nil
}
func (s *stubObjectStore) SignUpload(ctx context.Context, key, ct string, expiry time.Duration) (string, error) {
	if s.signUploadFn != nil {
		return s.signUploadFn(ctx, key, ct, expiry)
	}
	return "https://signed-upload-url", nil
}
func (s *stubObjectStore) SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if s.signDownloadFn != nil {
		return s.signDownloadFn(ctx, key, expiry)
	}
	return "https://signed-download-url", nil
}
func (s *stubObjectStore) Copy(ctx context.Context, src, dst string) error {
	if s.copyFn != nil {
		return s.copyFn(ctx, src, dst)
	}
	return nil
}
func (s *stubObjectStore) EnsureBucket(ctx context.Context, bucket string) error {
	if s.ensureBucketFn != nil {
		return s.ensureBucketFn(ctx, bucket)
	}
	return nil
}

// storageHandler builds a StorageV1Handler for tests. JWTKeys is nil —
// tests call handler methods directly, bypassing jwtAuth middleware.
func storageHandler(storageCfg map[string]domain.Bucket, db domain.Database, store domain.ObjectStore) *StorageV1Handler {
	return &StorageV1Handler{
		cfg:     &domain.Config{Storage: storageCfg},
		db:      db,
		logger:  slog.Default(),
		storage: store,
		jwtKeys: nil,
	}
}
```

- [ ] **Step 2: Add bucket handler tests**

```go
func TestListBuckets_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := storageHandler(map[string]domain.Bucket{}, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/storage/v1/bucket", nil)
	setSession(c, domain.Session{Role: "service_role"})
	h.listBuckets(c)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result []any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty list, got %d items", len(result))
	}
}

func TestListBuckets_NonEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := map[string]domain.Bucket{
		"avatars": {Public: true},
		"docs":    {Public: false},
	}
	h := storageHandler(cfg, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/storage/v1/bucket", nil)
	setSession(c, domain.Session{Role: "service_role"})
	h.listBuckets(c)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result []map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result) != 2 {
		t.Errorf("expected 2 buckets, got %d", len(result))
	}
}

func TestGetBucket_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := storageHandler(map[string]domain.Bucket{"avatars": {Public: true}}, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "avatars"}}
	c.Request, _ = http.NewRequest("GET", "/storage/v1/bucket/avatars", nil)
	h.getBucket(c)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["id"] != "avatars" {
		t.Errorf("expected id=avatars, got %v", result["id"])
	}
}

func TestGetBucket_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := storageHandler(map[string]domain.Bucket{}, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "missing"}}
	c.Request, _ = http.NewRequest("GET", "/storage/v1/bucket/missing", nil)
	h.getBucket(c)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCreateBucket_NotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := storageHandler(map[string]domain.Bucket{}, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/storage/v1/bucket", strings.NewReader(`{"name":"test"}`))
	h.createBucket(c)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpdateBucket_NotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := storageHandler(map[string]domain.Bucket{}, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "avatars"}}
	c.Request, _ = http.NewRequest("PUT", "/storage/v1/bucket/avatars", nil)
	h.updateBucket(c)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDeleteBucket_NotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := storageHandler(map[string]domain.Bucket{}, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "avatars"}}
	c.Request, _ = http.NewRequest("DELETE", "/storage/v1/bucket/avatars", nil)
	h.deleteBucket(c)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestEmptyBucket_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := storageHandler(map[string]domain.Bucket{}, &stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "missing"}}
	c.Request, _ = http.NewRequest("POST", "/storage/v1/bucket/missing/empty", nil)
	h.emptyBucket(c)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestEmptyBucket_DeletesObjects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{
		queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
			return []map[string]any{{"name": "photo.jpg"}}, nil
		},
	}
	var deleted []string
	store := &stubObjectStore{
		deleteFn: func(ctx context.Context, key string) error {
			deleted = append(deleted, key)
			return nil
		},
	}
	h := storageHandler(map[string]domain.Bucket{"avatars": {Public: false}}, db, store)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "avatars"}}
	c.Request, _ = http.NewRequest("POST", "/storage/v1/bucket/avatars/empty", nil)
	setSession(c, domain.Session{Role: "service_role"})
	h.emptyBucket(c)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(deleted) != 1 || deleted[0] != "avatars/photo.jpg" {
		t.Errorf("expected avatars/photo.jpg deleted, got %v", deleted)
	}
}
```

- [ ] **Step 3: Run bucket tests**

```sh
go test -race -run "TestListBuckets|TestGetBucket|TestCreateBucket|TestUpdateBucket|TestDeleteBucket|TestEmptyBucket" ./internal/adapter/http/...
```
Expected: PASS

---

### Task 10: Add storage_v1_handler_test.go (object handlers)

**Files:**
- Modify: `internal/adapter/http/storage_v1_handler_test.go`

- [ ] **Step 1: Add signed URL test**

```go
func TestCreateSignedURL_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return map[string]any{"name": "photo.jpg"}, nil
		},
	}
	store := &stubObjectStore{
		signDownloadFn: func(ctx context.Context, key string, expiry time.Duration) (string, error) {
			return "https://signed.example.com/photo.jpg?token=abc", nil
		},
	}
	h := storageHandler(map[string]domain.Bucket{"avatars": {Public: false}}, db, store)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{
		{Key: "bucket", Value: "avatars"},
		{Key: "path", Value: "/photo.jpg"},
	}
	c.Request, _ = http.NewRequest("POST", "/storage/v1/object/sign/avatars/photo.jpg",
		strings.NewReader(`{"expiresIn":3600}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSession(c, domain.Session{Role: "service_role"})
	h.createSignedURL(c)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["signedURL"] == nil {
		t.Errorf("expected signedURL in response, got %v", result)
	}
}

func TestCreateSignedURL_ObjectNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return nil, nil // object does not exist
		},
	}
	h := storageHandler(map[string]domain.Bucket{"avatars": {Public: false}}, db, &stubObjectStore{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{
		{Key: "bucket", Value: "avatars"},
		{Key: "path", Value: "/missing.jpg"},
	}
	c.Request, _ = http.NewRequest("POST", "/storage/v1/object/sign/avatars/missing.jpg",
		strings.NewReader(`{"expiresIn":3600}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSession(c, domain.Session{Role: "service_role"})
	h.createSignedURL(c)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run all storage tests**

```sh
go test -race -run "Storage|Bucket|SignedURL|Upload|Object" ./internal/adapter/http/...
```
Expected: PASS

- [ ] **Step 3: Commit Phase 4 Go tests**

```sh
git add internal/adapter/http/storage_v1_handler_test.go
git commit -m "test(http): add StorageV1Handler unit tests"
```

---

### Task 11: Add TableDetail.test.tsx

**Files:**
- Create: `dashboard/src/pages/TableDetail.test.tsx`

- [ ] **Step 1: Create the test file**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { TableDetail } from "./TableDetail";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {
    todos: {
      fields: {
        id: { type: "uuid", primary: true, nullable: false },
        title: { type: "text", nullable: false },
      },
      indexes: [],
      rls: { enabled: true, policies: [] },
      searchable: [],
    },
  },
  auth: null,
  storage: {},
  rpc: {},
  functions: {},
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderTableDetail(config: Config, tableName = "todos") {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter initialEntries={[`/tables/${tableName}`]}>
        <Routes>
          <Route path="/tables/:name" element={<TableDetail />} />
        </Routes>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("TableDetail", () => {
  it("renders column names for a table with fields", () => {
    renderTableDetail(baseConfig);
    expect(screen.getByText("id")).toBeInTheDocument();
    expect(screen.getByText("title")).toBeInTheDocument();
  });

  it("shows not-found message for a missing table", () => {
    renderTableDetail(baseConfig, "nonexistent");
    expect(screen.getByText(/not found/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test**

```sh
cd dashboard && npm test -- --reporter=verbose TableDetail
```
Expected: PASS

---

### Task 12: Add RpcDetail.test.tsx

**Files:**
- Create: `dashboard/src/pages/RpcDetail.test.tsx`

- [ ] **Step 1: Create the test file**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { RpcDetail } from "./RpcDetail";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  rpc: {
    get_todos: {
      args: [{ name: "user_id", type: "uuid" }],
      returns: "setof todos",
      body: "SELECT * FROM todos WHERE user_id = $1",
      language: "sql",
      volatility: "stable",
      security: "invoker",
    },
    no_args_fn: {
      args: [],
      returns: "text",
      body: "SELECT 'hello'",
      language: "sql",
      volatility: "immutable",
      security: "invoker",
    },
  },
  functions: {},
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderRpcDetail(config: Config, fnName = "get_todos") {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter initialEntries={[`/rpc/${fnName}`]}>
        <Routes>
          <Route path="/rpc/:name" element={<RpcDetail />} />
        </Routes>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("RpcDetail", () => {
  it("renders function name in the page header", () => {
    renderRpcDetail(baseConfig);
    expect(screen.getByText("get_todos")).toBeInTheDocument();
  });

  it("renders argument name", () => {
    renderRpcDetail(baseConfig);
    expect(screen.getByDisplayValue("user_id")).toBeInTheDocument();
  });

  it("renders return type", () => {
    renderRpcDetail(baseConfig);
    expect(screen.getByDisplayValue("setof todos")).toBeInTheDocument();
  });

  it("handles RPC with no arguments", () => {
    renderRpcDetail(baseConfig, "no_args_fn");
    expect(screen.getByText("no_args_fn")).toBeInTheDocument();
  });

  it("shows not-found message for missing function", () => {
    renderRpcDetail(baseConfig, "nonexistent");
    expect(screen.getByText(/not found/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test**

```sh
cd dashboard && npm test -- --reporter=verbose RpcDetail
```
Expected: PASS

---

### Task 13: Add StorageDetail.test.tsx

**Files:**
- Create: `dashboard/src/pages/StorageDetail.test.tsx`

- [ ] **Step 1: Create the test file**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { StorageDetail } from "./StorageDetail";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {
    avatars: {
      public: true,
      max_size: "5MB",
      types: ["image/jpeg", "image/png"],
      rls: [],
    },
  },
  rpc: {},
  functions: {},
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderStorageDetail(config: Config, bucketName = "avatars") {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter initialEntries={[`/storage/${bucketName}`]}>
        <Routes>
          <Route path="/storage/:name" element={<StorageDetail />} />
        </Routes>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("StorageDetail", () => {
  it("renders bucket name in the page header", () => {
    renderStorageDetail(baseConfig);
    expect(screen.getByText("avatars")).toBeInTheDocument();
  });

  it("renders the max file size field", () => {
    renderStorageDetail(baseConfig);
    expect(screen.getByDisplayValue("5MB")).toBeInTheDocument();
  });

  it("shows not-found message for a missing bucket", () => {
    renderStorageDetail(baseConfig, "nonexistent");
    expect(screen.getByText(/not found/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test**

```sh
cd dashboard && npm test -- --reporter=verbose StorageDetail
```
Expected: PASS

---

### Task 14: Add Providers.test.tsx

**Files:**
- Create: `dashboard/src/pages/Providers.test.tsx`

- [ ] **Step 1: Create the test file**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Providers } from "./Providers";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  rpc: {},
  functions: {},
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderProviders(config: Config) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter>
        <Providers />
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("Providers", () => {
  it("renders Email Provider section", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("Email Provider")).toBeInTheDocument();
  });

  it("renders Storage Provider section", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("Storage Provider")).toBeInTheDocument();
  });

  it("renders email provider options (Resend, SendGrid)", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("Resend")).toBeInTheDocument();
    expect(screen.getByText("SendGrid")).toBeInTheDocument();
  });

  it("renders storage provider options (AWS S3)", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("AWS S3")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test**

```sh
cd dashboard && npm test -- --reporter=verbose Providers
```
Expected: PASS

---

### Task 15: Add FunctionDetail.test.tsx; run full dashboard suite

**Files:**
- Create: `dashboard/src/pages/FunctionDetail.test.tsx`

- [ ] **Step 1: Create the test file**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { FunctionDetail } from "./FunctionDetail";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  rpc: {},
  functions: {
    process_image: {
      runtime: "node",
      file: "functions/process-image.js",
      auth_required: true,
      timeout: "60s",
    },
  },
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderFunctionDetail(config: Config, fnName = "process_image") {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter initialEntries={[`/functions/${fnName}`]}>
        <Routes>
          <Route path="/functions/:name" element={<FunctionDetail />} />
        </Routes>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("FunctionDetail", () => {
  it("renders function name in the page header", () => {
    renderFunctionDetail(baseConfig);
    expect(screen.getByText("process_image")).toBeInTheDocument();
  });

  it("renders the file path", () => {
    renderFunctionDetail(baseConfig);
    expect(screen.getByDisplayValue("functions/process-image.js")).toBeInTheDocument();
  });

  it("renders the timeout", () => {
    renderFunctionDetail(baseConfig);
    expect(screen.getByDisplayValue("60s")).toBeInTheDocument();
  });

  it("shows not-found message for a missing function", () => {
    renderFunctionDetail(baseConfig, "nonexistent");
    expect(screen.getByText(/not found/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run all dashboard tests**

```sh
cd dashboard && npm test
```
Expected: PASS — Phase 4 dashboard green gate.

- [ ] **Step 3: Commit Phase 4 dashboard tests**

```sh
git add dashboard/src/pages/TableDetail.test.tsx \
  dashboard/src/pages/RpcDetail.test.tsx \
  dashboard/src/pages/StorageDetail.test.tsx \
  dashboard/src/pages/Providers.test.tsx \
  dashboard/src/pages/FunctionDetail.test.tsx
git commit -m "test(dashboard): add detail page tests for TableDetail, RpcDetail, StorageDetail, Providers, FunctionDetail"
```

---

## Phase 5 — Auth service extraction

### Task 16: Add domain/auth.go with AuthService interface

**Files:**
- Create: `internal/domain/auth.go`
- Create: `internal/domain/errors.go` (if it doesn't exist)

- [ ] **Step 1: Check for existing error sentinels**

```sh
grep -rn "ErrNotFound\|ErrUnauthorized" internal/domain/
```

If they don't exist, create `internal/domain/errors.go`:
```go
package domain

import "errors"

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
)
```

- [ ] **Step 2: Create `internal/domain/auth.go`**

```go
package domain

import "context"

// CreateUserParams holds fields for auth user creation.
type CreateUserParams struct {
	Email          string
	Phone          string
	Password       string // bcrypt hash; empty = no password auth
	AppMetadata    map[string]any
	UserMetadata   map[string]any
	EmailConfirmed bool
	PhoneConfirmed bool
}

// UpdateUserParams holds fields for auth user updates. Nil pointer = no change.
type UpdateUserParams struct {
	Email          *string
	Phone          *string
	Password       *string // bcrypt hash; nil = no change
	AppMetadata    map[string]any
	UserMetadata   map[string]any
	EmailConfirmed *bool
	Banned         *bool
}

// AuthService is the port for all authentication data operations.
// The raw-map return type mirrors auth.users rows so AuthHandler.buildUser()
// can format them for the Supabase-compatible wire response unchanged.
type AuthService interface {
	// User lifecycle — return the raw auth.users row
	CreateUser(ctx context.Context, p CreateUserParams) (map[string]any, error)
	GetUserByID(ctx context.Context, id string) (map[string]any, error)
	GetUserByEmail(ctx context.Context, email string) (map[string]any, error)
	GetUserByPhone(ctx context.Context, phone string) (map[string]any, error)
	UpdateUser(ctx context.Context, id string, p UpdateUserParams) (map[string]any, error)
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, page, perPage int) ([]map[string]any, int, error)

	// Password
	VerifyPassword(ctx context.Context, email, password string) (map[string]any, error) // returns user row if valid
	SetPassword(ctx context.Context, userID, bcryptHash string) error

	// Sessions & tokens
	CreateSession(ctx context.Context, userID string) (sessionID, refreshToken string, err error)
	VerifyRefreshToken(ctx context.Context, token string) (userRow map[string]any, sessionID string, err error)
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeAllUserSessions(ctx context.Context, userID string) error

	// OTP / magic link / verification codes
	CreateOTPCode(ctx context.Context, userID, kind string) (token, code string, err error)
	VerifyOTPToken(ctx context.Context, token, kind string) (map[string]any, error)
	VerifyOTPCode(ctx context.Context, userID, kind, code string) error

	// PKCE flow
	CreateFlowState(ctx context.Context, provider, codeChallenge, codeChallengeMethod string) (authCode string, err error)
	GetFlowState(ctx context.Context, authCode string) (codeChallenge, method, userID string, err error)
	DeleteFlowState(ctx context.Context, authCode string) error

	// Identity linking
	GetOrCreateIdentity(ctx context.Context, provider, providerID string, userMeta map[string]any) (userRow map[string]any, created bool, err error)
	ListIdentities(ctx context.Context, userID string) ([]map[string]any, error)
	DeleteIdentity(ctx context.Context, userID, provider string) error

	// MFA
	CreateFactor(ctx context.Context, userID, factorType, friendlyName string) (map[string]any, error)
	VerifyFactor(ctx context.Context, factorID, code string) error
	DeleteFactor(ctx context.Context, factorID string) error
	ListFactors(ctx context.Context, userID string) ([]map[string]any, error)

	// Audit
	RecordSignIn(ctx context.Context, userID string)
}
```

- [ ] **Step 3: Verify compilation**

```sh
go build ./internal/domain/...
```
Expected: success

- [ ] **Step 4: Commit**

```sh
git add internal/domain/auth.go internal/domain/errors.go
git commit -m "feat(domain): add AuthService port interface"
```

---

### Task 17: Create internal/adapter/auth/ package skeleton

**Files:**
- Create: `internal/adapter/auth/service.go`
- Create: `internal/adapter/auth/helpers.go`
- Create: `internal/adapter/auth/tokens.go`
- Create: `internal/adapter/auth/password.go`

- [ ] **Step 1: Create `service.go`**

```go
// Package auth implements the domain.AuthService port using Postgres.
package auth

import (
	"log/slog"

	"github.com/instancez/instancez/internal/domain"
)

// Service implements domain.AuthService via direct Postgres queries.
type Service struct {
	db     domain.Database
	cfg    *domain.Config
	logger *slog.Logger
}

// NewService creates an AuthService backed by db.
func NewService(db domain.Database, cfg *domain.Config, logger *slog.Logger) *Service {
	return &Service{db: db, cfg: cfg, logger: logger}
}
```

- [ ] **Step 2: Create `helpers.go`**

```go
package auth

import (
	"encoding/json"
	"fmt"
	"time"
)

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprintf("%v", s)
	}
}

func asTimeString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func decodeJSONB(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	switch j := v.(type) {
	case map[string]any:
		return j
	case []byte:
		var m map[string]any
		_ = json.Unmarshal(j, &m)
		return m
	case string:
		var m map[string]any
		_ = json.Unmarshal([]byte(j), &m)
		return m
	default:
		return map[string]any{}
	}
}

// jsonbArg marshals a map for use as a JSONB Postgres parameter.
func jsonbArg(m map[string]any) []byte {
	if m == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(m)
	return b
}
```

- [ ] **Step 3: Create `tokens.go`**

```go
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
)

// generateRandomToken generates a URL-safe random token of the given byte length.
func generateRandomToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateNumericCode generates a numeric OTP of the given digit count.
func generateNumericCode(digits int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}
```

- [ ] **Step 4: Create `password.go`**

```go
package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword hashes a plaintext password with bcrypt cost 10.
func HashPassword(plaintext string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

func checkPassword(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}
```

- [ ] **Step 5: Verify compilation**

```sh
go build ./internal/adapter/auth/...
```
Expected: success

---

### Task 18: Implement user lifecycle + password methods in the auth service

**Files:**
- Modify: `internal/adapter/auth/service.go`

- [ ] **Step 1: Add imports and user method implementations**

The SQL for each method should be extracted from the corresponding section in `auth_handler.go`. Look for the SQL constant `userSelectCols` (~line 1061 of auth_handler.go) and user-related query calls.

Add to `service.go`:

```go
import (
	"context"
	"fmt"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// userSelectCols is the SELECT column list for auth.users rows.
// Must match what buildUser() in auth_handler.go expects.
// The exact value should be copied from the const at the top of auth_handler.go.
const userSelectCols = `id::text, email, email_verified, phone, phone_verified,
	raw_app_meta_data, raw_user_meta_data, created_at, updated_at, last_sign_in_at,
	email_confirmed_at, is_anonymous, banned_until, confirmation_sent_at`

func (s *Service) GetUserByEmail(ctx context.Context, email string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx, `SELECT `+userSelectCols+` FROM auth.users WHERE email = $1`, email)
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	if row == nil {
		return nil, domain.ErrNotFound
	}
	return row, nil
}

func (s *Service) GetUserByID(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx, `SELECT `+userSelectCols+` FROM auth.users WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	if row == nil {
		return nil, domain.ErrNotFound
	}
	return row, nil
}

func (s *Service) GetUserByPhone(ctx context.Context, phone string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx, `SELECT `+userSelectCols+` FROM auth.users WHERE phone = $1`, phone)
	if err != nil {
		return nil, fmt.Errorf("get user by phone: %w", err)
	}
	if row == nil {
		return nil, domain.ErrNotFound
	}
	return row, nil
}

func (s *Service) CreateUser(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx, `
		INSERT INTO auth.users (email, phone, encrypted_password, raw_app_meta_data, raw_user_meta_data,
		                        email_confirmed_at, phone_confirmed_at, created_at, updated_at)
		VALUES ($1, NULLIF($2,''), $3, $4, $5,
		        CASE WHEN $6 THEN now() END,
		        CASE WHEN $7 THEN now() END,
		        now(), now())
		RETURNING `+userSelectCols,
		p.Email, p.Phone, p.Password,
		jsonbArg(p.AppMetadata), jsonbArg(p.UserMetadata),
		p.EmailConfirmed, p.PhoneConfirmed,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return row, nil
}

func (s *Service) UpdateUser(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error) {
	sets := []string{"updated_at = now()"}
	args := []any{}
	idx := 1

	if p.Email != nil {
		sets = append(sets, fmt.Sprintf("email = $%d", idx))
		args = append(args, *p.Email)
		idx++
	}
	if p.Phone != nil {
		sets = append(sets, fmt.Sprintf("phone = NULLIF($%d, '')", idx))
		args = append(args, *p.Phone)
		idx++
	}
	if p.Password != nil {
		sets = append(sets, fmt.Sprintf("encrypted_password = $%d", idx))
		args = append(args, *p.Password)
		idx++
	}
	if p.AppMetadata != nil {
		sets = append(sets, fmt.Sprintf("raw_app_meta_data = $%d", idx))
		args = append(args, jsonbArg(p.AppMetadata))
		idx++
	}
	if p.UserMetadata != nil {
		sets = append(sets, fmt.Sprintf("raw_user_meta_data = raw_user_meta_data || $%d", idx))
		args = append(args, jsonbArg(p.UserMetadata))
		idx++
	}
	if p.EmailConfirmed != nil && *p.EmailConfirmed {
		sets = append(sets, "email_confirmed_at = COALESCE(email_confirmed_at, now())")
		sets = append(sets, "email_verified = true")
	}
	if p.Banned != nil {
		if *p.Banned {
			sets = append(sets, "banned_until = 'infinity'::timestamptz")
		} else {
			sets = append(sets, "banned_until = NULL")
		}
	}

	args = append(args, id)
	query := fmt.Sprintf(`UPDATE auth.users SET %s WHERE id = $%d RETURNING `+userSelectCols,
		strings.Join(sets, ", "), idx)
	row, err := s.db.QueryRow(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	return row, nil
}

func (s *Service) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, id)
	return err
}

func (s *Service) ListUsers(ctx context.Context, page, perPage int) ([]map[string]any, int, error) {
	offset := page * perPage
	rows, err := s.db.Query(ctx, `
		SELECT `+userSelectCols+`, count(*) OVER() AS total_count
		FROM auth.users ORDER BY created_at DESC LIMIT $1 OFFSET $2`, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	var total int
	for _, row := range rows {
		if total == 0 {
			total = int(asInt64(row["total_count"]))
		}
	}
	return rows, total, nil
}

func (s *Service) VerifyPassword(ctx context.Context, email, password string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		`SELECT encrypted_password, `+userSelectCols+` FROM auth.users WHERE email = $1`, email)
	if err != nil || row == nil {
		return nil, domain.ErrUnauthorized
	}
	hash := asString(row["encrypted_password"])
	if err := checkPassword(hash, password); err != nil {
		return nil, domain.ErrUnauthorized
	}
	delete(row, "encrypted_password")
	return row, nil
}

func (s *Service) SetPassword(ctx context.Context, userID, bcryptHash string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE auth.users SET encrypted_password = $1, updated_at = now() WHERE id = $2`,
		bcryptHash, userID)
	return err
}
```

**Note on `userSelectCols`:** The exact column list must match what `auth_handler.go`'s `buildUser()` function reads. Compare the constant in this file against what `buildUser()` reads (lines ~1704–1741 of auth_handler.go) and adjust if needed.

- [ ] **Step 2: Verify compilation**

```sh
go build ./internal/adapter/auth/...
```
Expected: success

---

### Task 19: Implement session, OTP, PKCE, identity, MFA methods; move OAuth helpers

**Files:**
- Modify: `internal/adapter/auth/service.go`
- Create: `internal/adapter/auth/oauth.go`

- [ ] **Step 1: Add session methods to service.go**

```go
func (s *Service) CreateSession(ctx context.Context, userID string) (sessionID, refreshToken string, err error) {
	refreshToken, err = generateRandomToken(40)
	if err != nil {
		return "", "", err
	}
	row, err := s.db.QueryRow(ctx, `
		INSERT INTO auth.sessions (id, user_id, created_at, updated_at, not_after)
		VALUES (gen_random_uuid(), $1, now(), now(), now() + interval '7 days')
		RETURNING id::text`,
		userID,
	)
	if err != nil {
		return "", "", fmt.Errorf("create session: %w", err)
	}
	sessionID = asString(row["id"])

	_, err = s.db.Exec(ctx, `
		INSERT INTO auth.refresh_tokens (token, user_id, session_id, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())`,
		refreshToken, userID, sessionID,
	)
	if err != nil {
		return "", "", fmt.Errorf("create refresh token: %w", err)
	}
	return sessionID, refreshToken, nil
}

func (s *Service) VerifyRefreshToken(ctx context.Context, token string) (map[string]any, string, error) {
	row, err := s.db.QueryRow(ctx, `
		SELECT rt.session_id::text, rt.user_id::text, rt.revoked
		FROM auth.refresh_tokens rt WHERE rt.token = $1`,
		token,
	)
	if err != nil || row == nil {
		return nil, "", domain.ErrUnauthorized
	}
	if revoked, _ := row["revoked"].(bool); revoked {
		return nil, "", domain.ErrUnauthorized
	}
	userID := asString(row["user_id"])
	sessionID := asString(row["session_id"])
	userRow, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	return userRow, sessionID, nil
}

func (s *Service) RevokeSession(ctx context.Context, sessionID string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE auth.refresh_tokens SET revoked = true, updated_at = now()
		WHERE session_id = $1`, sessionID); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM auth.sessions WHERE id = $1`, sessionID)
	return err
}

func (s *Service) RevokeAllUserSessions(ctx context.Context, userID string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE auth.refresh_tokens SET revoked = true, updated_at = now()
		WHERE user_id = $1`, userID); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM auth.sessions WHERE user_id = $1`, userID)
	return err
}

func (s *Service) RecordSignIn(ctx context.Context, userID string) {
	s.db.Exec(ctx, `UPDATE auth.users SET last_sign_in_at = now(), updated_at = now() WHERE id = $1`, userID)
}
```

- [ ] **Step 2: Add OTP, PKCE, identity, and MFA methods to service.go**

```go
func (s *Service) CreateOTPCode(ctx context.Context, userID, kind string) (token, code string, err error) {
	token, err = generateRandomToken(32)
	if err != nil {
		return "", "", err
	}
	code, err = generateNumericCode(6)
	if err != nil {
		return "", "", err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO auth.otp_tokens (user_id, kind, token, otp, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
		ON CONFLICT (user_id, kind) DO UPDATE SET token = $3, otp = $4, updated_at = now()`,
		userID, kind, token, code,
	)
	return token, code, err
}

func (s *Service) VerifyOTPToken(ctx context.Context, token, kind string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx, `
		SELECT user_id::text FROM auth.otp_tokens
		WHERE token = $1 AND kind = $2 AND created_at > now() - interval '24 hours'`,
		token, kind,
	)
	if err != nil || row == nil {
		return nil, domain.ErrUnauthorized
	}
	userID := asString(row["user_id"])
	s.db.Exec(ctx, `DELETE FROM auth.otp_tokens WHERE token = $1`, token)
	return s.GetUserByID(ctx, userID)
}

func (s *Service) VerifyOTPCode(ctx context.Context, userID, kind, code string) error {
	row, err := s.db.QueryRow(ctx, `
		SELECT otp FROM auth.otp_tokens
		WHERE user_id = $1 AND kind = $2 AND created_at > now() - interval '10 minutes'`,
		userID, kind,
	)
	if err != nil || row == nil {
		return domain.ErrUnauthorized
	}
	if asString(row["otp"]) != code {
		return domain.ErrUnauthorized
	}
	s.db.Exec(ctx, `DELETE FROM auth.otp_tokens WHERE user_id = $1 AND kind = $2`, userID, kind)
	return nil
}

func (s *Service) CreateFlowState(ctx context.Context, provider, codeChallenge, codeChallengeMethod string) (string, error) {
	authCode, err := generateRandomToken(32)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO auth.flow_state (id, auth_code, provider, code_challenge, code_challenge_method, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, now(), now())`,
		authCode, provider, codeChallenge, codeChallengeMethod,
	)
	return authCode, err
}

func (s *Service) GetFlowState(ctx context.Context, authCode string) (codeChallenge, method, userID string, err error) {
	row, err := s.db.QueryRow(ctx, `
		SELECT code_challenge, code_challenge_method, COALESCE(user_id::text, '')
		FROM auth.flow_state WHERE auth_code = $1 AND created_at > now() - interval '10 minutes'`,
		authCode,
	)
	if err != nil || row == nil {
		return "", "", "", domain.ErrNotFound
	}
	return asString(row["code_challenge"]), asString(row["code_challenge_method"]), asString(row["user_id"]), nil
}

func (s *Service) DeleteFlowState(ctx context.Context, authCode string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.flow_state WHERE auth_code = $1`, authCode)
	return err
}

func (s *Service) GetOrCreateIdentity(ctx context.Context, provider, providerID string, userMeta map[string]any) (map[string]any, bool, error) {
	row, err := s.db.QueryRow(ctx, `
		SELECT user_id::text FROM auth.identities WHERE provider = $1 AND provider_id = $2`,
		provider, providerID,
	)
	if err == nil && row != nil {
		userRow, err := s.GetUserByID(ctx, asString(row["user_id"]))
		return userRow, false, err
	}
	userRow, err := s.CreateUser(ctx, domain.CreateUserParams{
		AppMetadata:  map[string]any{"provider": provider, "providers": []string{provider}},
		UserMetadata: userMeta,
	})
	if err != nil {
		return nil, false, err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO auth.identities (provider, provider_id, user_id, identity_data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())`,
		provider, providerID, asString(userRow["id"]), jsonbArg(userMeta),
	)
	return userRow, true, err
}

func (s *Service) ListIdentities(ctx context.Context, userID string) ([]map[string]any, error) {
	return s.db.Query(ctx, `
		SELECT provider, provider_id, identity_data FROM auth.identities WHERE user_id = $1`, userID)
}

func (s *Service) DeleteIdentity(ctx context.Context, userID, provider string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.identities WHERE user_id = $1 AND provider = $2`, userID, provider)
	return err
}

func (s *Service) CreateFactor(ctx context.Context, userID, factorType, friendlyName string) (map[string]any, error) {
	return s.db.QueryRow(ctx, `
		INSERT INTO auth.mfa_factors (user_id, factor_type, friendly_name, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'unverified', now(), now())
		RETURNING id::text, status, friendly_name`,
		userID, factorType, friendlyName,
	)
}

func (s *Service) VerifyFactor(ctx context.Context, factorID, code string) error {
	_, err := s.db.Exec(ctx, `UPDATE auth.mfa_factors SET status = 'verified', updated_at = now() WHERE id = $1`, factorID)
	return err
}

func (s *Service) DeleteFactor(ctx context.Context, factorID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.mfa_factors WHERE id = $1`, factorID)
	return err
}

func (s *Service) ListFactors(ctx context.Context, userID string) ([]map[string]any, error) {
	return s.db.Query(ctx, `
		SELECT id::text, factor_type, friendly_name, status FROM auth.mfa_factors WHERE user_id = $1`, userID)
}
```

- [ ] **Step 3: Create `oauth.go` by moving OAuth helpers from auth_handler.go**

In `auth_handler.go`, find the functions `fetchGitHubUser`, `fetchGitHubPrimaryEmail`, `exchangeCode`, and any Google OAuth equivalents. Move them verbatim to `internal/adapter/auth/oauth.go`:

```go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// FetchGitHubUser fetches the authenticated user's profile from GitHub.
// (Move the body of fetchGitHubUser from auth_handler.go here verbatim.)
func FetchGitHubUser(ctx context.Context, accessToken string) (map[string]any, error) {
	// ... copy from auth_handler.go ...
}

// FetchGitHubPrimaryEmail fetches the primary verified email from GitHub.
// (Move the body of fetchGitHubPrimaryEmail from auth_handler.go here verbatim.)
func FetchGitHubPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	// ... copy from auth_handler.go ...
}

// ExchangeCode exchanges an OAuth authorization code for tokens at the given tokenURL.
// (Move the body of exchangeCode from auth_handler.go here verbatim.)
func ExchangeCode(ctx context.Context, tokenURL, clientID, clientSecret, code, redirectURI string) (map[string]any, error) {
	// ... copy from auth_handler.go ...
}
```

Delete those functions from `auth_handler.go`. In `auth_handler.go` update call sites to use `adapterauth.FetchGitHubUser(...)` etc., where the import alias is `adapterauth "github.com/instancez/instancez/internal/adapter/auth"`.

- [ ] **Step 4: Verify compilation**

```sh
go build ./internal/adapter/auth/... ./internal/adapter/http/...
```
Expected: success (the service implements the interface now)

---

### Task 20: Update AuthHandler to use AuthService; wire up in ServerDeps and engine

**Files:**
- Modify: `internal/adapter/http/auth_handler.go`
- Modify: `internal/adapter/http/server.go`
- Modify: `internal/app/engine.go`
- Modify: `internal/cli/serve.go`
- Modify: `internal/cli/dev.go`

- [ ] **Step 1: Add `authSvc domain.AuthService` to `AuthHandler` and `ServerDeps`**

In `server.go`, add to `ServerDeps`:
```go
AuthService domain.AuthService // replaces direct DB auth queries in AuthHandler
```

In `auth_handler.go`, add to `AuthHandler` struct:
```go
authSvc domain.AuthService
```

In `NewAuthHandler`:
```go
return &AuthHandler{
    cfg:     deps.Config,
    db:      deps.DB.Database,
    logger:  deps.Logger,
    email:   deps.Email,
    jwtKeys: deps.JWTKeys,
    authSvc: deps.AuthService, // new
}
```

- [ ] **Step 2: Replace `h.db.*` calls with `h.authSvc.*` calls in auth_handler.go**

Work through `auth_handler.go` systematically. For each `h.db.QueryRow(ctx, "SELECT ... FROM auth.users WHERE email = $1", ...)`:

```go
// handlePasswordGrant — was:
row, err := h.db.QueryRow(ctx, "SELECT ... FROM auth.users WHERE email = $1", email)
// now:
row, err := h.authSvc.GetUserByEmail(ctx, email)

// handleRefreshGrant — was:
row, err := h.db.QueryRow(ctx, "SELECT ... FROM auth.refresh_tokens WHERE token = $1", token)
// now:
userRow, sessionID, err := h.authSvc.VerifyRefreshToken(ctx, token)

// handleSignupPassword — creating user was:
row, err := h.db.QueryRow(ctx, "INSERT INTO auth.users ... RETURNING ...", ...)
// now:
row, err := h.authSvc.CreateUser(ctx, domain.CreateUserParams{...})

// buildSession — was direct INSERT into auth.sessions + auth.refresh_tokens:
// now:
sessionID, refreshToken, err := h.authSvc.CreateSession(ctx, userID)

// sendVerificationEmail — was INSERT into auth.otp_tokens:
// now:
token, code, err := h.authSvc.CreateOTPCode(ctx, userID, kind)

// handleGetUser — was:
row, err := h.db.QueryRow(ctx, "SELECT ... FROM auth.users WHERE id = $1", userID)
// now:
row, err := h.authSvc.GetUserByID(ctx, userID)

// handleUpdateUser — was direct UPDATE:
// now:
row, err := h.authSvc.UpdateUser(ctx, userID, domain.UpdateUserParams{...})

// handleLogout — was DELETE/UPDATE on sessions:
// now:
h.authSvc.RevokeSession(ctx, sessionID)

// RecordSignIn — was fire-and-forget UPDATE:
// now:
h.authSvc.RecordSignIn(ctx, userID)
```

The `buildUser(userID, row)` method in `auth_handler.go` continues to work unchanged since `authSvc` returns `map[string]any` rows.

- [ ] **Step 3: Remove `h.db` from `AuthHandler` if it's no longer used**

After converting all call sites, check if `h.db` is still referenced. If not, remove the `db` field from the struct and its initialization in `NewAuthHandler`. If some code paths still use it, note them as technical debt.

- [ ] **Step 4: Wire AuthService in `engine.go`**

In `internal/app/engine.go`, in the method where `ServerDeps` is constructed (search for `instancezhttp.ServerDeps{`):

```go
import adapterauth "github.com/instancez/instancez/internal/adapter/auth"

// Before constructing deps:
authSvc := adapterauth.NewService(e.authDB.Database, e.cfg, e.logger)

deps := instancezhttp.ServerDeps{
    Config:          e.cfg,
    DB:              e.authDB,
    Logger:          e.logger,
    // ... existing fields ...
    AuthService:     authSvc,
}
```

- [ ] **Step 5: Wire AuthService in `cli/serve.go` and `cli/dev.go`**

In both files, after the DB connections are opened:
```go
import adapterauth "github.com/instancez/instancez/internal/adapter/auth"

authSvc := adapterauth.NewService(authDB.Database, cfg, logger)

deps := instancezhttp.ServerDeps{
    // ... existing fields ...
    AuthService: authSvc,
}
```

- [ ] **Step 6: Run full test suite**

```sh
go test -race ./...
```
Expected: PASS

---

### Task 21: Update auth_handler_test.go; run all tests

**Files:**
- Modify: `internal/adapter/http/auth_handler_test.go`

- [ ] **Step 1: Add `stubAuthService` implementing `domain.AuthService`**

```go
type stubAuthService struct {
	createUserFn          func(ctx context.Context, p domain.CreateUserParams) (map[string]any, error)
	getUserByEmailFn      func(ctx context.Context, email string) (map[string]any, error)
	getUserByIDFn         func(ctx context.Context, id string) (map[string]any, error)
	getUserByPhoneFn      func(ctx context.Context, phone string) (map[string]any, error)
	updateUserFn          func(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error)
	verifyPasswordFn      func(ctx context.Context, email, password string) (map[string]any, error)
	createSessionFn       func(ctx context.Context, userID string) (string, string, error)
	verifyRefreshTokenFn  func(ctx context.Context, token string) (map[string]any, string, error)
	createOTPCodeFn       func(ctx context.Context, userID, kind string) (string, string, error)
	verifyOTPTokenFn      func(ctx context.Context, token, kind string) (map[string]any, error)
}

func (s *stubAuthService) CreateUser(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
	if s.createUserFn != nil {
		return s.createUserFn(ctx, p)
	}
	return map[string]any{"id": "user-1", "email": p.Email}, nil
}
func (s *stubAuthService) GetUserByID(ctx context.Context, id string) (map[string]any, error) {
	if s.getUserByIDFn != nil {
		return s.getUserByIDFn(ctx, id)
	}
	return nil, domain.ErrNotFound
}
func (s *stubAuthService) GetUserByEmail(ctx context.Context, email string) (map[string]any, error) {
	if s.getUserByEmailFn != nil {
		return s.getUserByEmailFn(ctx, email)
	}
	return nil, domain.ErrNotFound
}
func (s *stubAuthService) GetUserByPhone(ctx context.Context, phone string) (map[string]any, error) {
	if s.getUserByPhoneFn != nil {
		return s.getUserByPhoneFn(ctx, phone)
	}
	return nil, domain.ErrNotFound
}
func (s *stubAuthService) UpdateUser(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error) {
	if s.updateUserFn != nil {
		return s.updateUserFn(ctx, id, p)
	}
	return map[string]any{"id": id}, nil
}
func (s *stubAuthService) DeleteUser(ctx context.Context, id string) error { return nil }
func (s *stubAuthService) ListUsers(ctx context.Context, page, perPage int) ([]map[string]any, int, error) {
	return nil, 0, nil
}
func (s *stubAuthService) VerifyPassword(ctx context.Context, email, password string) (map[string]any, error) {
	if s.verifyPasswordFn != nil {
		return s.verifyPasswordFn(ctx, email, password)
	}
	return nil, domain.ErrUnauthorized
}
func (s *stubAuthService) SetPassword(ctx context.Context, userID, bcryptHash string) error { return nil }
func (s *stubAuthService) CreateSession(ctx context.Context, userID string) (string, string, error) {
	if s.createSessionFn != nil {
		return s.createSessionFn(ctx, userID)
	}
	return "sess-1", "refresh-token-1", nil
}
func (s *stubAuthService) VerifyRefreshToken(ctx context.Context, token string) (map[string]any, string, error) {
	if s.verifyRefreshTokenFn != nil {
		return s.verifyRefreshTokenFn(ctx, token)
	}
	return nil, "", domain.ErrUnauthorized
}
func (s *stubAuthService) RevokeSession(ctx context.Context, sessionID string) error { return nil }
func (s *stubAuthService) RevokeAllUserSessions(ctx context.Context, userID string) error {
	return nil
}
func (s *stubAuthService) CreateOTPCode(ctx context.Context, userID, kind string) (string, string, error) {
	if s.createOTPCodeFn != nil {
		return s.createOTPCodeFn(ctx, userID, kind)
	}
	return "token-1", "123456", nil
}
func (s *stubAuthService) VerifyOTPToken(ctx context.Context, token, kind string) (map[string]any, error) {
	if s.verifyOTPTokenFn != nil {
		return s.verifyOTPTokenFn(ctx, token, kind)
	}
	return nil, domain.ErrUnauthorized
}
func (s *stubAuthService) VerifyOTPCode(ctx context.Context, userID, kind, code string) error {
	return nil
}
func (s *stubAuthService) CreateFlowState(ctx context.Context, provider, codeChallenge, method string) (string, error) {
	return "auth-code-1", nil
}
func (s *stubAuthService) GetFlowState(ctx context.Context, authCode string) (string, string, string, error) {
	return "", "", "", domain.ErrNotFound
}
func (s *stubAuthService) DeleteFlowState(ctx context.Context, authCode string) error { return nil }
func (s *stubAuthService) GetOrCreateIdentity(ctx context.Context, provider, providerID string, userMeta map[string]any) (map[string]any, bool, error) {
	return map[string]any{"id": "user-1"}, false, nil
}
func (s *stubAuthService) ListIdentities(ctx context.Context, userID string) ([]map[string]any, error) {
	return nil, nil
}
func (s *stubAuthService) DeleteIdentity(ctx context.Context, userID, provider string) error {
	return nil
}
func (s *stubAuthService) CreateFactor(ctx context.Context, userID, factorType, friendlyName string) (map[string]any, error) {
	return nil, nil
}
func (s *stubAuthService) VerifyFactor(ctx context.Context, factorID, code string) error { return nil }
func (s *stubAuthService) DeleteFactor(ctx context.Context, factorID string) error       { return nil }
func (s *stubAuthService) ListFactors(ctx context.Context, userID string) ([]map[string]any, error) {
	return nil, nil
}
func (s *stubAuthService) RecordSignIn(ctx context.Context, userID string) {}
```

- [ ] **Step 2: Replace `stubDB`-based handler construction in existing auth tests**

For each test that constructs `&AuthHandler{cfg: ..., db: &stubDB{...}}`, replace with `&AuthHandler{cfg: ..., authSvc: &stubAuthService{...}}`.

Map SQL-level stubs to method-level stubs. Example:

```go
// OLD:
db := &stubDB{
    queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
        if strings.Contains(q, "SELECT ... FROM auth.users WHERE email") {
            return map[string]any{"id": "user-1", "email": "a@b.com", ...}, nil
        }
        ...
    },
}
h := &AuthHandler{cfg: ..., db: db, ...}

// NEW:
svc := &stubAuthService{
    getUserByEmailFn: func(ctx context.Context, email string) (map[string]any, error) {
        return map[string]any{"id": "user-1", "email": "a@b.com", ...}, nil
    },
}
h := &AuthHandler{cfg: ..., authSvc: svc, ...}
```

- [ ] **Step 3: Run full test suite**

```sh
go test -race ./...
```
Expected: PASS

- [ ] **Step 4: Run the supabase-js compat suite to confirm wire contract intact**

```sh
go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...
```
Expected: PASS (requires Docker)

- [ ] **Step 5: Run dashboard tests**

```sh
cd dashboard && npm test
```
Expected: PASS

- [ ] **Step 6: Commit Phase 5**

```sh
git add internal/domain/auth.go internal/domain/errors.go \
  internal/adapter/auth/ \
  internal/adapter/http/auth_handler.go \
  internal/adapter/http/auth_handler_test.go \
  internal/adapter/http/server.go \
  internal/app/engine.go \
  internal/cli/serve.go \
  internal/cli/dev.go
git commit -m "refactor(auth): extract domain.AuthService port and internal/adapter/auth/ implementation"
```

---

## Self-review

**Spec coverage check:**
- Phase 1 CRUD dedup ✓ (Tasks 1–2)
- Phase 2 PostgREST package split ✓ (Tasks 3–7)
- Phase 3 golangci-lint ✓ (Task 8)
- Phase 4 storage handler tests ✓ (Tasks 9–10), dashboard detail page tests ✓ (Tasks 11–15)
- Phase 5 auth service extraction ✓ (Tasks 16–21)

**Notes for implementer:**
- Task 4 (where.go): `parseWhereWith` function body has a `for key, values := range c.Request.URL.Query()` loop — change `c.Request.URL.Query()` to `vals`.
- Task 6 (builders.go): Some functions reference `SelectItem` (from select.go) — ensure the import is in scope after the move.
- Task 18 (user lifecycle): The exact value of `userSelectCols` must match what `buildUser()` reads. Cross-check against lines ~1704–1741 of auth_handler.go before finalising.
- Task 20 (AuthHandler update): Work through auth_handler.go function by function. Keep `buildUser()`, `buildSession()` (but rewrite to call authSvc), utility functions (`asString`, `asTimeString`, `decodeJSONB`, `renderAuthTemplate`, `issuer`, `baseURL`) in the handler file — they are either wire-formatting helpers or still used by the handler directly.
- The `asString`/`decodeJSONB` helpers exist in both `auth_handler.go` and `internal/adapter/auth/helpers.go` — that's intentional, they serve different packages.

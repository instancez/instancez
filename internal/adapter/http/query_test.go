package http

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/adapter/http/postgrest"
	"github.com/instancez/instancez/internal/domain"
)

func TestBuildSelectQuery(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
			{Name: "status", Type: "text"},
		},
	}

	tests := []struct {
		name     string
		qp       *QueryParams
		wantSQL  string
		wantArgs int
	}{
		{
			name: "basic select all",
			qp: &QueryParams{
				Limit:  20,
				Offset: 0,
			},
			wantSQL:  "SELECT todos.* FROM todos LIMIT 20 OFFSET 0",
			wantArgs: 0,
		},
		{
			name: "with filter",
			qp: &QueryParams{
				Where:  postgrest.AndLeaves(postgrest.Filter{Column: "status", Operator: "eq", Value: "active"}),
				Limit:  20,
				Offset: 0,
			},
			wantSQL:  "SELECT todos.* FROM todos WHERE status = $1 LIMIT 20 OFFSET 0",
			wantArgs: 1,
		},
		{
			name: "with order",
			qp: &QueryParams{
				Order:  []OrderClause{{Column: "created_at", Desc: true}},
				Limit:  20,
				Offset: 0,
			},
			wantSQL:  "SELECT todos.* FROM todos ORDER BY created_at DESC LIMIT 20 OFFSET 0",
			wantArgs: 0,
		},
		{
			name: "with multiple filters and order",
			qp: &QueryParams{
				Where: postgrest.AndLeaves(
					postgrest.Filter{Column: "status", Operator: "eq", Value: "active"},
					postgrest.Filter{Column: "priority", Operator: "gte", Value: "3"},
				),
				Order:  []OrderClause{{Column: "priority", Desc: true}, {Column: "title", Desc: false}},
				Limit:  10,
				Offset: 20,
			},
			wantSQL:  "SELECT todos.* FROM todos WHERE (status = $1 AND priority >= $2) ORDER BY priority DESC, title ASC LIMIT 10 OFFSET 20",
			wantArgs: 2,
		},
		{
			name: "with field selection",
			qp: &QueryParams{
				Select: []string{"id", "title"},
				Limit:  20,
				Offset: 0,
			},
			wantSQL:  "SELECT todos.id, todos.title FROM todos LIMIT 20 OFFSET 0",
			wantArgs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := buildSelectQuery("todos", tt.qp, table)
			if sql != tt.wantSQL {
				t.Errorf("SQL = %q\nwant %q", sql, tt.wantSQL)
			}
			if len(args) != tt.wantArgs {
				t.Errorf("args count = %d, want %d", len(args), tt.wantArgs)
			}
		})
	}
}

func TestBuildInsertQuery(t *testing.T) {
	record := map[string]any{
		"title":  "Test task",
		"status": "pending",
	}

	sql, args := buildInsertQuery("todos", record, false)
	if !strings.Contains(sql, "INSERT INTO todos") {
		t.Errorf("SQL should contain INSERT INTO todos, got: %s", sql)
	}
	if !strings.Contains(sql, "status") || !strings.Contains(sql, "title") {
		t.Errorf("SQL should contain all column names, got: %s", sql)
	}
	if len(args) != 2 {
		t.Errorf("args count = %d, want 2", len(args))
	}

	sqlRet, _ := buildInsertQuery("todos", record, true)
	if !strings.HasSuffix(sqlRet, "RETURNING *") {
		t.Error("expected RETURNING * when returning=true")
	}
}

func TestBuildUpdateQuery(t *testing.T) {
	updates := map[string]any{"status": "done", "title": "Updated"}
	where := postgrest.AndLeaves(postgrest.Filter{Column: "id", Operator: "eq", Value: "42"})

	sql, args := buildUpdateQuery("todos", updates, where, false)
	if !strings.Contains(sql, "UPDATE todos SET") {
		t.Errorf("SQL should contain UPDATE, got: %s", sql)
	}
	if !strings.Contains(sql, "WHERE") {
		t.Errorf("SQL should contain WHERE, got: %s", sql)
	}
	// 2 SET values + 1 filter
	if len(args) != 3 {
		t.Errorf("args count = %d, want 3", len(args))
	}

	sqlRet, _ := buildUpdateQuery("todos", updates, where, true)
	if !strings.HasSuffix(sqlRet, "RETURNING *") {
		t.Error("expected RETURNING * when returning=true")
	}
}

func TestBuildDeleteQuery(t *testing.T) {
	where := postgrest.AndLeaves(postgrest.Filter{Column: "status", Operator: "eq", Value: "archived"})

	sql, args := buildDeleteQuery("todos", where, false)
	if sql != "DELETE FROM todos WHERE status = $1" {
		t.Errorf("SQL = %q, want DELETE FROM todos WHERE status = $1", sql)
	}
	if len(args) != 1 {
		t.Errorf("args count = %d, want 1", len(args))
	}
}

func TestBuildDeleteQuery_NoFilters(t *testing.T) {
	sql, args := buildDeleteQuery("todos", nil, false)
	if sql != "DELETE FROM todos" {
		t.Errorf("SQL = %q, want DELETE FROM todos", sql)
	}
	if len(args) != 0 {
		t.Errorf("args count = %d, want 0", len(args))
	}
}

func TestParseSelectParam(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"*", []string{"*"}},
		{"id,title,status", []string{"id", "title", "status"}},
		{"id,title,author(id,name)", []string{"id", "title", "author(id,name)"}},
		{"*,author(*),tags(*)", []string{"*", "author(*)", "tags(*)"}},
		{"*,author(*,company(*))", []string{"*", "author(*,company(*))"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSelectParam(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFindUnknownFields(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial"},
			{Name: "title", Type: "text"},
			{Name: "status", Type: "text"},
		},
	}

	record := map[string]any{
		"title":   "test",
		"status":  "active",
		"unknown": "field",
		"another": "bad",
	}

	unknowns := findUnknownFields(record, table.FieldMap())
	if len(unknowns) != 2 {
		t.Fatalf("expected 2 unknown fields, got %d: %v", len(unknowns), unknowns)
	}
	if unknowns[0] != "another" || unknowns[1] != "unknown" {
		t.Errorf("unknowns = %v, want [another, unknown]", unknowns)
	}
}

func TestFindUnknownFields_AllKnown(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "title", Type: "text"},
		},
	}
	unknowns := findUnknownFields(map[string]any{"title": "test"}, table.FieldMap())
	if len(unknowns) != 0 {
		t.Errorf("expected no unknowns, got %v", unknowns)
	}
}

func TestParseReturnPrefer(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "minimal"},
		{"return=minimal", "minimal"},
		{"return=representation", "representation"},
		{"return=headers-only", "headers-only"},
		{"count=exact, return=representation", "representation"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseReturnPrefer(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCountPrefer(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"count=exact", "exact"},
		{"count=planned", "planned"},
		{"count=estimated", "estimated"},
		{"return=representation, count=exact", "exact"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseCountPrefer(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMissingPrefer(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},
		{"missing=default", true},
		{"return=representation, missing=default", true},
		{"missing=null", false},
		{"resolution=merge-duplicates", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseMissingPrefer(tt.input)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Documents the existing permissive behavior of renderRowTuples: missing
// columns are always substituted with DEFAULT, regardless of whether the
// client sent `Prefer: missing=default`. The Preference-Applied echo in
// handleCreate/handleUpsert exists to confirm this to clients that probe.
func TestRenderRowTuples_MissingColumnEmitsDefault(t *testing.T) {
	records := []map[string]any{
		{"name": "alice", "age": 30},
		{"name": "bob"}, // age missing
	}
	cols := []string{"name", "age"}
	args, rows := renderRowTuples(records, cols, 1)

	if len(rows) != 2 {
		t.Fatalf("got %d row tuples, want 2", len(rows))
	}
	if rows[0] != "($1, $2)" {
		t.Errorf("row0 = %q, want ($1, $2)", rows[0])
	}
	if rows[1] != "($3, DEFAULT)" {
		t.Errorf("row1 = %q, want ($3, DEFAULT)", rows[1])
	}
	if len(args) != 3 {
		t.Errorf("args len = %d, want 3", len(args))
	}
}

func TestParseEmbedParam(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantCols []string
	}{
		{"author(id,name)", "author", []string{"id", "name"}},
		{"author(*)", "author", nil},
		{"author()", "author", nil},
		{"tags", "tags", nil},
		{"company(id,name,address)", "company", []string{"id", "name", "address"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			name, _, cols, _, _ := parseEmbedParam(tt.input)
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if tt.wantCols == nil {
				if cols != nil {
					t.Errorf("cols = %v, want nil", cols)
				}
			} else {
				if len(cols) != len(tt.wantCols) {
					t.Fatalf("cols len = %d, want %d", len(cols), len(tt.wantCols))
				}
				for i := range cols {
					if cols[i] != tt.wantCols[i] {
						t.Errorf("cols[%d] = %q, want %q", i, cols[i], tt.wantCols[i])
					}
				}
			}
		})
	}
}

func TestResolveEmbeds_BelongsTo(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
			{Name: "author_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
		},
	}
	allTables := map[string]domain.Table{
		"todos": table,
		"users": {
			Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "name", Type: "text"},
			},
		},
	}

	embeds, err := resolveEmbeds("todos", table, []string{"author(id,name)"}, allTables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(embeds))
	}
	e := embeds[0]
	if e.Name != "author" {
		t.Errorf("name = %q, want author", e.Name)
	}
	if e.FKColumn != "author_id" {
		t.Errorf("FKColumn = %q, want author_id", e.FKColumn)
	}
	if e.RefTable != "users" {
		t.Errorf("RefTable = %q, want users", e.RefTable)
	}
	if e.RefColumn != "id" {
		t.Errorf("RefColumn = %q, want id", e.RefColumn)
	}
	if e.IsReverse {
		t.Error("should not be reverse")
	}
}

func TestResolveEmbeds_HasMany(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "name", Type: "text"},
		},
	}
	allTables := map[string]domain.Table{
		"users": table,
		"todos": {
			Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
				{Name: "title", Type: "text"},
			},
		},
	}

	embeds, err := resolveEmbeds("users", table, []string{"todos(*)"}, allTables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(embeds))
	}
	e := embeds[0]
	if e.Name != "todos" {
		t.Errorf("name = %q, want todos", e.Name)
	}
	if !e.IsReverse {
		t.Error("should be reverse (has-many)")
	}
	if e.FKColumn != "user_id" {
		t.Errorf("FKColumn = %q, want user_id", e.FKColumn)
	}
}

func TestBuildSelectQuery_WithEmbed(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
			{Name: "author_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
		},
	}
	qp := &QueryParams{
		Select: []string{"id", "title", "author(id,name)"},
		Embeds: []Embed{
			{
				Name:      "author",
				Columns:   []string{"id", "name"},
				FKColumn:  "author_id",
				RefTable:  "users",
				RefColumn: "id",
			},
		},
		Limit:  20,
		Offset: 0,
	}

	sql, _ := buildSelectQuery("todos", qp, table)

	if !strings.Contains(sql, "LEFT JOIN users AS _emb_author") {
		t.Errorf("SQL should contain LEFT JOIN, got: %s", sql)
	}
	if !strings.Contains(sql, "todos.id") {
		t.Errorf("SQL should qualify base columns with table name, got: %s", sql)
	}
	if !strings.Contains(sql, "json_build_object") {
		t.Errorf("SQL should contain json_build_object for embed columns, got: %s", sql)
	}
}

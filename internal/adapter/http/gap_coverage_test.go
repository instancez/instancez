package http

import (
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

// Gap 1: belongs-to embed filter + outer WHERE — verify $N indices chain
// across the two different code paths (alias rewrite vs direct emission).
func TestBuildSelectQuery_BelongsToFilterAndOuterWhere_ArgOrdering(t *testing.T) {
	tables := postsAuthorTables()
	qp := &QueryParams{
		Select: []string{"*", "author(*)"},
		Embeds: []Embed{{
			Name:      "author",
			FKColumn:  "author_id",
			RefTable:  "authors",
			RefColumn: "id",
			Where:     andLeaves(Filter{Column: "name", Operator: "eq", Value: "bob"}),
		}},
		Where: andLeaves(Filter{Column: "status", Operator: "eq", Value: "published"}),
		Limit: 20,
	}
	sql, args := buildSelectQuery("posts", qp, tables["posts"])
	// Belongs-to filter args are collected first (during JOIN emission),
	// outer WHERE args come second. So _emb_author.name should bind $1 and
	// status should bind $2.
	if !strings.Contains(sql, "_emb_author.name = $1") {
		t.Errorf("expected belongs-to filter at $1, got: %s", sql)
	}
	if !strings.Contains(sql, "status = $2") {
		t.Errorf("expected outer filter at $2, got: %s", sql)
	}
	// WHERE clause must contain both, joined by AND (order-independent).
	if !strings.Contains(sql, " WHERE ") {
		t.Fatalf("missing WHERE: %s", sql)
	}
	whereIdx := strings.Index(sql, " WHERE ")
	whereClause := sql[whereIdx:]
	if !strings.Contains(whereClause, "status = $2") || !strings.Contains(whereClause, "_emb_author.name = $1") {
		t.Errorf("WHERE should contain both clauses: %s", whereClause)
	}
	if len(args) != 2 || args[0] != "bob" || args[1] != "published" {
		t.Errorf("args = %v", args)
	}
}

// Gap 2: has-many embed with a non-"id" RefColumn — the LATERAL subquery
// must reference the actual parent PK, not a hardcoded "id".
func TestBuildSelectQuery_HasManyWithNonIdRefColumn(t *testing.T) {
	table := domain.Table{
		Fields: map[string]domain.Field{
			"uuid": {Type: "uuid", PrimaryKey: true},
			"name": {Type: "text"},
		},
	}
	qp := &QueryParams{
		Select: []string{"*", "posts(*)"},
		Embeds: []Embed{{
			Name: "posts", IsReverse: true,
			FKColumn: "author_uuid", RefTable: "posts", RefColumn: "uuid",
		}},
		Limit: 20,
	}
	sql, _ := buildSelectQuery("authors", qp, table)
	if !strings.Contains(sql, "posts.author_uuid = authors.uuid") {
		t.Errorf("expected join on uuid, got: %s", sql)
	}
	if strings.Contains(sql, "authors.id") {
		t.Errorf("should not reference authors.id: %s", sql)
	}
}

// Gap 3: direct table-driven tests for parseOrderValue — it's extracted
// code used both by top-level order and embed-scoped order.
func TestParseOrderValue_Direct(t *testing.T) {
	table := testTable()
	t.Run("single", func(t *testing.T) {
		c, err := parseOrderValue("title", table)
		if err != nil || len(c) != 1 || c[0].Column != "title" || c[0].Desc {
			t.Errorf("got %+v err=%v", c, err)
		}
	})
	t.Run("multi with modifiers", func(t *testing.T) {
		c, err := parseOrderValue("priority.desc.nullslast,title.asc", table)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(c) != 2 {
			t.Fatalf("got %d", len(c))
		}
		if c[0].Column != "priority" || !c[0].Desc || c[0].Nulls != "last" {
			t.Errorf("c[0] = %+v", c[0])
		}
		if c[1].Column != "title" || c[1].Desc || c[1].Nulls != "" {
			t.Errorf("c[1] = %+v", c[1])
		}
	})
	t.Run("nulls only", func(t *testing.T) {
		c, err := parseOrderValue("priority.nullsfirst", table)
		if err != nil || c[0].Nulls != "first" || c[0].Desc {
			t.Errorf("got %+v err=%v", c, err)
		}
	})
	t.Run("rejects unknown column", func(t *testing.T) {
		if _, err := parseOrderValue("bogus.asc", table); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("rejects injection", func(t *testing.T) {
		if _, err := parseOrderValue("id; DROP TABLE users", table); err == nil {
			t.Error("expected error")
		}
	})
}

// Gap 4: columns= that strips every key — ensure we don't build a
// malformed INSERT INTO t () VALUES () statement.
func TestFilterRecordsByColumns_AllDropped(t *testing.T) {
	records := []map[string]any{{"extra": 1, "unknown": "x"}}
	cols := map[string]bool{"title": true}
	out := filterRecordsByColumns(records, cols)
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	if len(out[0]) != 0 {
		t.Errorf("expected empty map, got %+v", out[0])
	}
}

func TestBuildBulkInsertQuery_RejectsAllEmptyRecords(t *testing.T) {
	// With no columns across any record, unionColumns returns nothing and
	// we'd otherwise emit "INSERT INTO t () VALUES ()". Guard against it.
	records := []map[string]any{{}}
	cols := unionColumns(records)
	if len(cols) != 0 {
		t.Fatalf("expected 0 cols, got %v", cols)
	}
	// Validate the handler-level guard: recordsAllEmpty should detect this.
	if !recordsAllEmpty(records) {
		t.Error("expected recordsAllEmpty=true")
	}
}

// Gap 5: CSV parser with quoted fields containing commas and newlines —
// verify the stdlib reader handles them and our wrapper doesn't interfere.
func TestCsvReadRecords_QuotedCommasAndNewlines(t *testing.T) {
	body := "title,note\n\"a,b\",\"line1\nline2\"\n"
	recs, err := csvReadRecords([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d rows, want 1", len(recs))
	}
	if recs[0]["title"] != "a,b" {
		t.Errorf("title = %q", recs[0]["title"])
	}
	if recs[0]["note"] != "line1\nline2" {
		t.Errorf("note = %q", recs[0]["note"])
	}
}

package http

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

type recordingTx struct {
	queries []string
	args    [][]any
	row     map[string]any
	err     error
}

func (r *recordingTx) Query(_ context.Context, q string, _ ...any) ([]map[string]any, error) {
	r.queries = append(r.queries, q)
	return []map[string]any{{"QUERY PLAN": "Aggregate  (cost=1.00..2.00 rows=7 width=8)"}}, r.err
}
func (r *recordingTx) QueryRow(_ context.Context, q string, args ...any) (map[string]any, error) {
	r.queries = append(r.queries, q)
	r.args = append(r.args, args)
	return r.row, r.err
}
func (r *recordingTx) Exec(context.Context, string, ...any) (int64, error) { return 0, nil }
func (r *recordingTx) Commit(context.Context) error                        { return nil }
func (r *recordingTx) Rollback(context.Context) error                      { return nil }

var _ domain.Tx = (*recordingTx)(nil)

func countFor(t *testing.T, table, raw, mode string, tx *recordingTx) (int, error) {
	t.Helper()
	tables := postsAuthorTables()
	qp, err := parseQueryParams(testContext(raw), table, tables[table], tables)
	if err != nil {
		t.Fatal(err)
	}
	return executeCount(context.Background(), tx, table, tables[table], qp, tables, mode)
}

func TestExecuteCount_ExactHonorsInnerHasMany(t *testing.T) {
	tx := &recordingTx{row: map[string]any{"count": int64(3)}}
	n, err := countFor(t, "authors", "select=*,posts!inner(*)&posts.status=eq.published&limit=2&offset=4", "exact", tx)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	q := tx.queries[0]
	for _, want := range []string{"SELECT COUNT(*) AS count FROM (", "EXISTS (SELECT 1 FROM posts", "status = $"} {
		if !strings.Contains(q, want) {
			t.Errorf("count SQL missing %q: %s", want, q)
		}
	}
	if strings.Contains(q, "LIMIT") || strings.Contains(q, "OFFSET") || strings.Contains(q, "ORDER BY") {
		t.Errorf("count must ignore paging/order: %s", q)
	}
}

func TestExecuteCount_ExactHonorsBelongsToFilter(t *testing.T) {
	tx := &recordingTx{row: map[string]any{"count": int64(0)}}
	if _, err := countFor(t, "posts", "select=*,authors!inner(*)&authors.active=eq.true", "exact", tx); err != nil {
		t.Fatal(err)
	}
	q := tx.queries[0]
	if !strings.Contains(q, "INNER JOIN authors AS _emb_authors") || !strings.Contains(q, "_emb_authors.active") {
		t.Errorf("count must include the belongs-to join and filter: %s", q)
	}
}

func TestExecuteCount_ErrorSurfaces(t *testing.T) {
	boom := errors.New("boom")
	for _, mode := range []string{"exact", "planned", "estimated"} {
		if _, err := countFor(t, "authors", "active=eq.true", mode, &recordingTx{err: boom}); !errors.Is(err, boom) {
			t.Errorf("%s: err=%v, want boom", mode, err)
		}
	}
}

func TestExecuteCount_PlannedAndEstimated(t *testing.T) {
	tx := &recordingTx{}
	n, err := countFor(t, "authors", "select=*,posts!inner(*)", "planned", tx)
	if err != nil || n != 7 || !strings.HasPrefix(tx.queries[0], "EXPLAIN SELECT") {
		t.Fatalf("planned: n=%d err=%v q=%v", n, err, tx.queries)
	}
	tx = &recordingTx{row: map[string]any{"count": int64(42)}}
	n, err = countFor(t, "authors", "", "estimated", tx)
	if err != nil || n != 42 || !strings.Contains(tx.queries[0], "to_regclass($1)") {
		t.Fatalf("estimated plain: n=%d err=%v q=%v", n, err, tx.queries)
	}
	for _, raw := range []string{"active=eq.true", "select=*,posts(*)", "select=active,count()"} {
		tx = &recordingTx{}
		if _, err := countFor(t, "authors", raw, "estimated", tx); err != nil || !strings.HasPrefix(tx.queries[0], "EXPLAIN ") {
			t.Fatalf("estimated %q must plan: err=%v q=%v", raw, err, tx.queries)
		}
	}
}

func TestExecuteCount_EstimatedQualifiesSchema(t *testing.T) {
	for schema, want := range map[string]string{"": "public.authors", "public": "public.authors", "billing": "billing.authors"} {
		tables := postsAuthorTables()
		tbl := tables["authors"]
		tbl.Schema = schema
		tx := &recordingTx{row: map[string]any{"count": int64(9)}}
		n, err := executeCount(context.Background(), tx, "authors", tbl, &QueryParams{}, tables, "estimated")
		if err != nil || n != 9 || len(tx.args) != 1 || tx.args[0][0] != want {
			t.Errorf("schema %q: n=%d err=%v args=%v, want %s", schema, n, err, tx.args, want)
		}
	}
}

func TestExecuteCount_DoesNotMutateQueryParams(t *testing.T) {
	tables := postsAuthorTables()
	qp, _ := parseQueryParams(testContext("limit=2&offset=4&order=name"), "authors", tables["authors"], tables)
	_, _ = executeCount(context.Background(), &recordingTx{row: map[string]any{"count": int64(1)}}, "authors", tables["authors"], qp, tables, "exact")
	if qp.Limit != 2 || qp.Offset != 4 || len(qp.Order) != 1 {
		t.Fatalf("qp mutated: %+v", qp)
	}
}

func TestExecuteCount_NilRowAndUnknownMode(t *testing.T) {
	if n, err := countFor(t, "authors", "", "exact", &recordingTx{}); err != nil || n != -1 {
		t.Fatalf("nil row: n=%d err=%v", n, err)
	}
	if n, err := countFor(t, "authors", "", "bogus", &recordingTx{}); err != nil || n != -1 {
		t.Fatalf("unknown mode: n=%d err=%v", n, err)
	}
}

func TestQueryCount_Types(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int
	}{{int64(5), 5}, {float64(9), 9}, {int64(0), 0}, {"5", -1}, {nil, -1}} {
		n, err := queryCount(context.Background(), &recordingTx{row: map[string]any{"count": tc.v}}, "q")
		if err != nil || n != tc.want {
			t.Errorf("%v: n=%d err=%v want %d", tc.v, n, err, tc.want)
		}
	}
}

func TestPlanRows(t *testing.T) {
	for _, tc := range []struct {
		rows []map[string]any
		want int
	}{
		{nil, -1},
		{[]map[string]any{}, -1},
		{[]map[string]any{{"QUERY PLAN": "Seq Scan on t  (cost=0.00..1.05 rows=5 width=4)"}}, 5},
		{[]map[string]any{{"QUERY PLAN": "Result  (cost=0.00..0.01 rows=0)"}}, 0},
		{[]map[string]any{{"QUERY PLAN": "no estimate"}}, -1},
		{[]map[string]any{{"QUERY PLAN": "Seq Scan rows=abc width=4"}}, -1},
		{[]map[string]any{{"QUERY PLAN": 12}}, -1},
	} {
		if got := planRows(tc.rows); got != tc.want {
			t.Errorf("%v: got %d want %d", tc.rows, got, tc.want)
		}
	}
}

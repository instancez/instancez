package postgrest

import (
	"testing"
)

func TestParseFilterValue(t *testing.T) {
	tests := []struct {
		input      string
		wantOp     string
		wantVal    string
		wantConfig string
		wantErr    bool
	}{
		{"eq.active", "eq", "active", "", false},
		{"neq.done", "neq", "done", "", false},
		{"gt.5", "gt", "5", "", false},
		{"gte.10", "gte", "10", "", false},
		{"lt.100", "lt", "100", "", false},
		{"lte.50", "lte", "50", "", false},
		{"like.*task*", "like", "*task*", "", false},
		{"ilike.*TASK*", "ilike", "*TASK*", "", false},
		{"match.^foo", "match", "^foo", "", false},
		{"imatch.^FOO", "imatch", "^FOO", "", false},
		{"is.null", "is", "null", "", false},
		{"is.true", "is", "true", "", false},
		{"isdistinct.null", "isdistinct", "null", "", false},
		{"in.(a,b,c)", "in", "(a,b,c)", "", false},
		{"plfts.search text", "plfts", "search text", "", false},
		{"fts(english).rápido & furioso", "fts", "rápido & furioso", "english", false},
		{"plfts(simple).dogs", "plfts", "dogs", "simple", false},
		{"cs.{urgent}", "cs", "{urgent}", "", false},
		{"cd.{a,b}", "cd", "{a,b}", "", false},
		{"is.false", "is", "false", "", false},
		{"is.unknown", "is", "unknown", "", false},
		{"invalid", "", "", "", true},
		{"unknown.value", "", "", "", true},
		{"fts().query", "", "", "", true},
		{"fts(bad;name).query", "", "", "", true},
		// `is` must reject anything that isn't a SQL truth keyword, otherwise
		// the value would be interpolated raw into the WHERE clause (SQLi).
		{"is.null) OR (1=1", "", "", "", true},
		{"is.1", "", "", "", true},
		{"is.(SELECT 1)", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			op, val, config, err := parseFilterValue(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if op != tt.wantOp {
				t.Errorf("op = %q, want %q", op, tt.wantOp)
			}
			if val != tt.wantVal {
				t.Errorf("val = %q, want %q", val, tt.wantVal)
			}
			if config != tt.wantConfig {
				t.Errorf("config = %q, want %q", config, tt.wantConfig)
			}
		})
	}
}

func TestBuildFilterCondition(t *testing.T) {
	tests := []struct {
		name    string
		filter  Filter
		wantSQL string
		wantN   int // number of args produced
	}{
		{
			name:    "eq",
			filter:  Filter{Column: "status", Operator: "eq", Value: "active"},
			wantSQL: "status = $1",
			wantN:   1,
		},
		{
			name:    "neq",
			filter:  Filter{Column: "status", Operator: "neq", Value: "done"},
			wantSQL: "status != $1",
			wantN:   1,
		},
		{
			name:    "gt",
			filter:  Filter{Column: "priority", Operator: "gt", Value: "3"},
			wantSQL: "priority > $1",
			wantN:   1,
		},
		{
			name:    "is null",
			filter:  Filter{Column: "deleted_at", Operator: "is", Value: "null"},
			wantSQL: "deleted_at IS NULL",
			wantN:   0,
		},
		{
			name:    "is true",
			filter:  Filter{Column: "active", Operator: "is", Value: "true"},
			wantSQL: "active IS TRUE",
			wantN:   0,
		},
		{
			name:    "in list",
			filter:  Filter{Column: "status", Operator: "in", Value: "(pending,active,done)"},
			wantSQL: "status IN ($1, $2, $3)",
			wantN:   3,
		},
		{
			name:    "plfts",
			filter:  Filter{Column: "title", Operator: "plfts", Value: "search terms"},
			wantSQL: "title @@ plainto_tsquery($1)",
			wantN:   1,
		},
		{
			name:    "fts",
			filter:  Filter{Column: "title", Operator: "fts", Value: "cats & dogs"},
			wantSQL: "title @@ to_tsquery($1)",
			wantN:   1,
		},
		{
			name:    "phfts",
			filter:  Filter{Column: "title", Operator: "phfts", Value: "exact phrase"},
			wantSQL: "title @@ phraseto_tsquery($1)",
			wantN:   1,
		},
		{
			name:    "wfts",
			filter:  Filter{Column: "title", Operator: "wfts", Value: "web search"},
			wantSQL: "title @@ websearch_to_tsquery($1)",
			wantN:   1,
		},
		{
			name:    "cs contains",
			filter:  Filter{Column: "tags", Operator: "cs", Value: "{urgent}"},
			wantSQL: "tags @> $1",
			wantN:   1,
		},
		{
			name:    "cd contained_by",
			filter:  Filter{Column: "tags", Operator: "cd", Value: "{a,b}"},
			wantSQL: "tags <@ $1",
			wantN:   1,
		},
		{
			name:    "like",
			filter:  Filter{Column: "title", Operator: "like", Value: "%task%"},
			wantSQL: "title LIKE $1",
			wantN:   1,
		},
		{
			name:    "ilike",
			filter:  Filter{Column: "title", Operator: "ilike", Value: "%Task%"},
			wantSQL: "title ILIKE $1",
			wantN:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, args, _ := buildFilterCondition(tt.filter, 1)
			if cond != tt.wantSQL {
				t.Errorf("SQL = %q, want %q", cond, tt.wantSQL)
			}
			if len(args) != tt.wantN {
				t.Errorf("args count = %d, want %d", len(args), tt.wantN)
			}
		})
	}
}

func TestSplitJSONBPath(t *testing.T) {
	tests := []struct {
		input string
		base  string
		steps []jsonPathStep
	}{
		{"metadata->>theme", "metadata", []jsonPathStep{{op: "->>", key: "theme"}}},
		{"metadata->nested", "metadata", []jsonPathStep{{op: "->", key: "nested"}}},
		{"data->>name", "data", []jsonPathStep{{op: "->>", key: "name"}}},
		{"simple_col", "simple_col", nil},
		{"tags", "tags", nil},
		{"data->items->0->>name", "data", []jsonPathStep{
			{op: "->", key: "items"},
			{op: "->", key: "0", isInt: true},
			{op: "->>", key: "name"},
		}},
		{"data->0", "data", []jsonPathStep{{op: "->", key: "0", isInt: true}}},
		{"data->>0", "data", []jsonPathStep{{op: "->>", key: "0", isInt: true}}},
		{"d->a->b->c", "d", []jsonPathStep{
			{op: "->", key: "a"},
			{op: "->", key: "b"},
			{op: "->", key: "c"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			base, steps := splitJSONBPath(tt.input)
			if base != tt.base {
				t.Errorf("base = %q, want %q", base, tt.base)
			}
			if len(steps) != len(tt.steps) {
				t.Fatalf("steps = %+v, want %+v", steps, tt.steps)
			}
			for i := range steps {
				if steps[i] != tt.steps[i] {
					t.Errorf("step[%d] = %+v, want %+v", i, steps[i], tt.steps[i])
				}
			}
		})
	}
}

func TestRenderJSONBSuffix_ArrayIndex(t *testing.T) {
	_, steps := splitJSONBPath("data->items->0->>name")
	got := renderJSONBSuffix(steps)
	want := "->'items'->0->>'name'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildFilterCondition_JSONB(t *testing.T) {
	tests := []struct {
		name    string
		filter  Filter
		wantSQL string
		wantN   int
	}{
		{
			name:    "jsonb text extract eq",
			filter:  Filter{Column: "metadata->>theme", Operator: "eq", Value: "dark"},
			wantSQL: "metadata->>'theme' = $1",
			wantN:   1,
		},
		{
			name:    "jsonb extract is null",
			filter:  Filter{Column: "metadata->>color", Operator: "is", Value: "null"},
			wantSQL: "metadata->>'color' IS NULL",
			wantN:   0,
		},
		{
			name:    "jsonb extract like",
			filter:  Filter{Column: "data->>name", Operator: "like", Value: "%test%"},
			wantSQL: "data->>'name' LIKE $1",
			wantN:   1,
		},
		{
			name:    "jsonb arrow extract",
			filter:  Filter{Column: "metadata->nested", Operator: "cs", Value: `{"a":1}`},
			wantSQL: "metadata->'nested' @> $1",
			wantN:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, args, _ := buildFilterCondition(tt.filter, 1)
			if cond != tt.wantSQL {
				t.Errorf("SQL = %q, want %q", cond, tt.wantSQL)
			}
			if len(args) != tt.wantN {
				t.Errorf("args count = %d, want %d", len(args), tt.wantN)
			}
		})
	}
}

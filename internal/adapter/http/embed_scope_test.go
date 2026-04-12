package http

import (
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

// postsAuthorTables builds a two-table fixture where `posts` has a
// belongs-to FK to `authors` and `authors` has an implicit has-many
// relation back to `posts`.
func postsAuthorTables() map[string]domain.Table {
	return map[string]domain.Table{
		"posts": {
			Fields: map[string]domain.Field{
				"id":        {Type: "bigserial", PrimaryKey: true},
				"title":     {Type: "text"},
				"status":    {Type: "text"},
				"author_id": {ForeignKey: &domain.ForeignKey{References: "authors.id"}},
			},
		},
		"authors": {
			Fields: map[string]domain.Field{
				"id":     {Type: "bigserial", PrimaryKey: true},
				"name":   {Type: "text"},
				"active": {Type: "bool"},
			},
		},
	}
}

func TestParseQueryParams_EmbedFilter_HasMany(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,posts(*)&posts.status=eq.published")
	qp, err := parseQueryParams(c, "authors", tables["authors"], tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(qp.Embeds))
	}
	emb := qp.Embeds[0]
	if !emb.IsReverse {
		t.Fatalf("expected has-many embed")
	}
	if emb.Where == nil || len(emb.Where.Children) != 1 {
		t.Fatalf("expected 1 embed filter, got %+v", emb.Where)
	}
	leaf := emb.Where.Children[0].Leaf
	if leaf == nil || leaf.Column != "status" || leaf.Operator != "eq" || leaf.Value != "published" {
		t.Errorf("leaf = %+v", leaf)
	}
}

func TestParseQueryParams_EmbedOrderAndLimit_HasMany(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,posts(*)&posts.order=title.asc&posts.limit=5")
	qp, err := parseQueryParams(c, "authors", tables["authors"], tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	emb := qp.Embeds[0]
	if len(emb.Order) != 1 || emb.Order[0].Column != "title" || emb.Order[0].Desc {
		t.Errorf("order = %+v", emb.Order)
	}
	if emb.Limit == nil || *emb.Limit != 5 {
		t.Errorf("limit = %+v", emb.Limit)
	}
}

func TestParseQueryParams_EmbedFilter_BelongsTo(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,author(*)&author.name=eq.bob")
	qp, err := parseQueryParams(c, "posts", tables["posts"], tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Embeds) != 1 || qp.Embeds[0].IsReverse {
		t.Fatalf("expected belongs-to embed, got %+v", qp.Embeds)
	}
	emb := qp.Embeds[0]
	if emb.Where == nil || len(emb.Where.Children) != 1 {
		t.Fatalf("expected 1 embed filter, got %+v", emb.Where)
	}
}

func TestParseQueryParams_EmbedOrderOnBelongsToRejected(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,author(*)&author.order=name.asc")
	if _, err := parseQueryParams(c, "posts", tables["posts"], tables); err == nil {
		t.Error("expected rejection: order not allowed on belongs-to embed")
	}
}

func TestParseQueryParams_EmbedLimitOnBelongsToRejected(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,author(*)&author.limit=3")
	if _, err := parseQueryParams(c, "posts", tables["posts"], tables); err == nil {
		t.Error("expected rejection: limit not allowed on belongs-to embed")
	}
}

func TestParseQueryParams_EmbedUnknownColumnRejected(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,posts(*)&posts.bogus=eq.1")
	if _, err := parseQueryParams(c, "authors", tables["authors"], tables); err == nil {
		t.Error("expected rejection for unknown column in embed filter")
	}
}

func TestParseQueryParams_EmbedInvalidLimitRejected(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,posts(*)&posts.limit=nope")
	if _, err := parseQueryParams(c, "authors", tables["authors"], tables); err == nil {
		t.Error("expected rejection for non-numeric embed limit")
	}
}

func TestParseQueryParams_EmbedDoesNotLeakIntoOuterWhere(t *testing.T) {
	tables := postsAuthorTables()
	// "posts.status" must not be treated as a top-level filter on authors.
	c := testContext("select=*,posts(*)&posts.status=eq.published")
	qp, err := parseQueryParams(c, "authors", tables["authors"], tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if qp.Where != nil && len(qp.Where.Children) > 0 {
		t.Errorf("outer WHERE should be empty, got %+v", qp.Where)
	}
}

func TestBuildSelectQuery_HasManyEmbedWithFilterOrderLimit(t *testing.T) {
	tables := postsAuthorTables()
	limit := 5
	qp := &QueryParams{
		Select: []string{"*", "posts(*)"},
		Embeds: []Embed{{
			Name: "posts", IsReverse: true,
			FKColumn: "author_id", RefTable: "posts", RefColumn: "id",
			Where: andLeaves(Filter{Column: "status", Operator: "eq", Value: "published"}),
			Order: []OrderClause{{Column: "title", Desc: false}},
			Limit: &limit,
		}},
		Limit: 20,
	}
	sql, args := buildSelectQuery("authors", qp, tables["authors"])

	if !strings.Contains(sql, "json_agg(") {
		t.Errorf("missing json_agg for has-many embed: %s", sql)
	}
	if !strings.Contains(sql, "FROM posts WHERE posts.author_id = authors.id AND status = $1") {
		t.Errorf("missing embed WHERE: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY title ASC") {
		t.Errorf("missing embed ORDER: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT 5") {
		t.Errorf("missing embed LIMIT: %s", sql)
	}
	if len(args) != 1 || args[0] != "published" {
		t.Errorf("args = %v", args)
	}
}

func TestBuildSelectQuery_BelongsToFilterGoesToOuterWhere(t *testing.T) {
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
		Limit: 20,
	}
	sql, args := buildSelectQuery("posts", qp, tables["posts"])
	if !strings.Contains(sql, "LEFT JOIN authors AS _emb_author") {
		t.Errorf("missing join: %s", sql)
	}
	if !strings.Contains(sql, "WHERE _emb_author.name = $1") {
		t.Errorf("expected belongs-to filter in outer WHERE, got: %s", sql)
	}
	if len(args) != 1 || args[0] != "bob" {
		t.Errorf("args = %v", args)
	}
}

func TestBuildSelectQuery_EmbedFilterAndOuterFilterArgOrdering(t *testing.T) {
	tables := postsAuthorTables()
	qp := &QueryParams{
		Select: []string{"*", "posts(*)"},
		Embeds: []Embed{{
			Name: "posts", IsReverse: true,
			FKColumn: "author_id", RefTable: "posts", RefColumn: "id",
			Where: andLeaves(Filter{Column: "status", Operator: "eq", Value: "published"}),
		}},
		Where: andLeaves(Filter{Column: "active", Operator: "eq", Value: "true"}),
		Limit: 20,
	}
	sql, args := buildSelectQuery("authors", qp, tables["authors"])
	// Embed filter takes $1, outer WHERE takes $2.
	if !strings.Contains(sql, "status = $1") {
		t.Errorf("embed filter should be $1, got: %s", sql)
	}
	if !strings.Contains(sql, "active = $2") {
		t.Errorf("outer filter should be $2, got: %s", sql)
	}
	if len(args) != 2 || args[0] != "published" || args[1] != "true" {
		t.Errorf("args = %v", args)
	}
}

func TestParseQueryParams_EmbedLogicTree_HasMany(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,posts(*)&posts.or=(status.eq.published,status.eq.draft)")
	qp, err := parseQueryParams(c, "authors", tables["authors"], tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	emb := qp.Embeds[0]
	if emb.Where == nil || len(emb.Where.Children) != 1 {
		t.Fatalf("expected 1 logic child, got %+v", emb.Where)
	}
	or := emb.Where.Children[0]
	if or.Op != "or" || len(or.Children) != 2 {
		t.Errorf("expected or with 2 leaves, got %+v", or)
	}
}

func TestParseQueryParams_EmbedLogicTree_RejectsBadColumn(t *testing.T) {
	tables := postsAuthorTables()
	c := testContext("select=*,posts(*)&posts.or=(status.eq.x,bogus.eq.y)")
	if _, err := parseQueryParams(c, "authors", tables["authors"], tables); err == nil {
		t.Error("expected rejection for unknown column inside embed logic list")
	}
}

func TestBuildSelectQuery_EmbedOrLogicEmitsOR(t *testing.T) {
	tables := postsAuthorTables()
	or := &WhereNode{Op: "or", Children: []*WhereNode{
		{Leaf: &Filter{Column: "status", Operator: "eq", Value: "published"}},
		{Leaf: &Filter{Column: "status", Operator: "eq", Value: "draft"}},
	}}
	qp := &QueryParams{
		Select: []string{"*", "posts(*)"},
		Embeds: []Embed{{
			Name: "posts", IsReverse: true,
			FKColumn: "author_id", RefTable: "posts", RefColumn: "id",
			Where: &WhereNode{Op: "and", Children: []*WhereNode{or}},
		}},
		Limit: 20,
	}
	sql, _ := buildSelectQuery("authors", qp, tables["authors"])
	if !strings.Contains(sql, "(status = $1 OR status = $2)") {
		t.Errorf("expected OR in subquery, got: %s", sql)
	}
}

// threeTableFixture builds authors → posts (has FK author_id) → comments (has FK post_id).
func threeTableFixture() map[string]domain.Table {
	return map[string]domain.Table{
		"authors": {
			Fields: map[string]domain.Field{
				"id":   {Type: "bigserial", PrimaryKey: true},
				"name": {Type: "text"},
			},
		},
		"posts": {
			Fields: map[string]domain.Field{
				"id":        {Type: "bigserial", PrimaryKey: true},
				"title":     {Type: "text"},
				"status":    {Type: "text"},
				"author_id": {ForeignKey: &domain.ForeignKey{References: "authors.id"}},
			},
		},
		"comments": {
			Fields: map[string]domain.Field{
				"id":      {Type: "bigserial", PrimaryKey: true},
				"body":    {Type: "text"},
				"post_id": {ForeignKey: &domain.ForeignKey{References: "posts.id"}},
			},
		},
	}
}

// --- Nested embed end-to-end parsing tests ---

func TestParseQueryParams_NestedEmbed(t *testing.T) {
	tables := threeTableFixture()
	c := testContext("select=*,posts(title,author(name))")
	qp, err := parseQueryParams(c, "authors", tables["authors"], tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(qp.Embeds))
	}
	emb := qp.Embeds[0]
	if emb.Name != "posts" || !emb.IsReverse {
		t.Fatalf("expected has-many posts, got %+v", emb)
	}
	if len(emb.Columns) != 1 || emb.Columns[0] != "title" {
		t.Errorf("cols = %v", emb.Columns)
	}
	if len(emb.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(emb.Children))
	}
	child := emb.Children[0]
	if child.Name != "author" || child.IsReverse {
		t.Errorf("expected belongs-to author child, got %+v", child)
	}
	if len(child.Columns) != 1 || child.Columns[0] != "name" {
		t.Errorf("child cols = %v", child.Columns)
	}
}

func TestParseQueryParams_SpreadEmbed(t *testing.T) {
	tables := threeTableFixture()
	c := testContext("select=title,...author(name)")
	qp, err := parseQueryParams(c, "posts", tables["posts"], tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(qp.Embeds))
	}
	emb := qp.Embeds[0]
	if !emb.Spread {
		t.Error("expected spread=true")
	}
	if emb.Name != "author" {
		t.Errorf("name = %q", emb.Name)
	}
}

func TestParseQueryParams_SpreadOnHasManyRejected(t *testing.T) {
	tables := threeTableFixture()
	c := testContext("select=*,...posts(*)")
	_, err := parseQueryParams(c, "authors", tables["authors"], tables)
	if err == nil {
		t.Error("expected rejection of spread on has-many")
	}
}

// --- Nested embed parsing tests ---

func TestParseEmbedParam_Nested(t *testing.T) {
	name, cols, nested, spread := parseEmbedParam("author(*,posts(*))")
	if name != "author" {
		t.Errorf("name = %q", name)
	}
	if len(cols) != 0 {
		t.Errorf("cols = %v, want empty (star)", cols)
	}
	if len(nested) != 1 || nested[0] != "posts(*)" {
		t.Errorf("nested = %v", nested)
	}
	if spread {
		t.Error("unexpected spread")
	}
}

func TestParseEmbedParam_NestedWithCols(t *testing.T) {
	name, cols, nested, _ := parseEmbedParam("author(name,posts(title))")
	if name != "author" {
		t.Errorf("name = %q", name)
	}
	if len(cols) != 1 || cols[0] != "name" {
		t.Errorf("cols = %v", cols)
	}
	if len(nested) != 1 || nested[0] != "posts(title)" {
		t.Errorf("nested = %v", nested)
	}
}

func TestParseEmbedParam_Spread(t *testing.T) {
	name, cols, _, spread := parseEmbedParam("...author(name)")
	if name != "author" {
		t.Errorf("name = %q", name)
	}
	if !spread {
		t.Error("expected spread=true")
	}
	if len(cols) != 1 || cols[0] != "name" {
		t.Errorf("cols = %v", cols)
	}
}

func TestParseEmbedParam_SpreadStar(t *testing.T) {
	name, cols, _, spread := parseEmbedParam("...author(*)")
	if name != "author" {
		t.Errorf("name = %q", name)
	}
	if !spread {
		t.Error("expected spread=true")
	}
	if len(cols) != 0 {
		t.Errorf("cols = %v, want nil", cols)
	}
}

func TestResolveEmbeds_NestedBelongsToHasMany(t *testing.T) {
	tables := threeTableFixture()
	// posts → author(*,posts(*)) : belongs-to author, with nested has-many posts
	// This is: from posts, embed author which itself nests posts (the reverse side)
	embeds, err := resolveEmbeds("posts", tables["posts"], []string{"author(*,posts(*))"}, tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(embeds))
	}
	emb := embeds[0]
	if emb.Name != "author" || emb.IsReverse {
		t.Errorf("expected belongs-to author, got %+v", emb)
	}
	if len(emb.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(emb.Children))
	}
	child := emb.Children[0]
	if child.Name != "posts" || !child.IsReverse {
		t.Errorf("expected has-many posts child, got %+v", child)
	}
}

func TestResolveEmbeds_NestedHasManyBelongsTo(t *testing.T) {
	tables := threeTableFixture()
	// authors → posts(title, author(*)) : has-many posts, with nested belongs-to author
	embeds, err := resolveEmbeds("authors", tables["authors"], []string{"posts(title,author(name))"}, tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(embeds))
	}
	emb := embeds[0]
	if !emb.IsReverse {
		t.Error("expected has-many")
	}
	if len(emb.Columns) != 1 || emb.Columns[0] != "title" {
		t.Errorf("cols = %v", emb.Columns)
	}
	if len(emb.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(emb.Children))
	}
	child := emb.Children[0]
	if child.Name != "author" || child.IsReverse {
		t.Errorf("expected belongs-to author child, got %+v", child)
	}
	if len(child.Columns) != 1 || child.Columns[0] != "name" {
		t.Errorf("child cols = %v", child.Columns)
	}
}

func TestResolveEmbeds_SpreadOnHasManyRejected(t *testing.T) {
	tables := threeTableFixture()
	_, err := resolveEmbeds("authors", tables["authors"], []string{"...posts(*)"}, tables)
	if err == nil {
		t.Error("expected rejection of spread on has-many")
	}
}

func TestResolveEmbeds_SpreadOnBelongsToOK(t *testing.T) {
	tables := threeTableFixture()
	embeds, err := resolveEmbeds("posts", tables["posts"], []string{"...author(name)"}, tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 || !embeds[0].Spread {
		t.Errorf("expected spread embed, got %+v", embeds)
	}
}

// --- Nested embed SQL generation tests ---

func TestBuildSelectQuery_HasManyWithNestedBelongsTo(t *testing.T) {
	tables := threeTableFixture()
	// authors → posts(title, author(name))
	qp := &QueryParams{
		Select: []string{"*", "posts(title,author(name))"},
		Embeds: []Embed{{
			Name: "posts", IsReverse: true,
			FKColumn: "author_id", RefTable: "posts", RefColumn: "id",
			Columns: []string{"title"},
			Children: []Embed{{
				Name: "author", FKColumn: "author_id", RefTable: "authors", RefColumn: "id",
				Columns: []string{"name"},
			}},
		}},
		Limit: 20,
	}
	sql, _ := buildSelectQueryFull("authors", qp, tables["authors"], tables)

	// The has-many subselect should use json_build_object with nested scalar subselect.
	if !strings.Contains(sql, "json_agg(json_build_object(") {
		t.Errorf("missing json_agg(json_build_object( in: %s", sql)
	}
	if !strings.Contains(sql, "'title', posts.title") {
		t.Errorf("missing scalar col in: %s", sql)
	}
	if !strings.Contains(sql, "'author', (SELECT json_build_object('name', authors.name) FROM authors WHERE authors.id = posts.author_id LIMIT 1)") {
		t.Errorf("missing nested belongs-to subselect in: %s", sql)
	}
}

func TestBuildSelectQuery_BelongsToWithNestedHasMany(t *testing.T) {
	tables := threeTableFixture()
	// posts → author(name, posts(*))
	qp := &QueryParams{
		Select: []string{"*", "author(name,posts(*))"},
		Embeds: []Embed{{
			Name: "author", FKColumn: "author_id", RefTable: "authors", RefColumn: "id",
			Columns: []string{"name"},
			Children: []Embed{{
				Name: "posts", IsReverse: true,
				FKColumn: "author_id", RefTable: "posts", RefColumn: "id",
			}},
		}},
		Limit: 20,
	}
	sql, _ := buildSelectQueryFull("posts", qp, tables["posts"], tables)

	// Should have a json_build_object with nested has-many subselect.
	if !strings.Contains(sql, "json_build_object(") {
		t.Errorf("missing json_build_object in: %s", sql)
	}
	if !strings.Contains(sql, "'name', _emb_author.name") {
		t.Errorf("missing scalar col in: %s", sql)
	}
	if !strings.Contains(sql, "'posts', (SELECT coalesce(json_agg(row_to_json(posts.*))") {
		t.Errorf("missing nested has-many subselect in: %s", sql)
	}
}

func TestBuildSelectQuery_SpreadBelongsTo(t *testing.T) {
	tables := threeTableFixture()
	// posts → ...author(name)
	qp := &QueryParams{
		Select: []string{"title", "...author(name)"},
		Embeds: []Embed{{
			Name: "author", FKColumn: "author_id", RefTable: "authors", RefColumn: "id",
			Columns: []string{"name"},
			Spread:  true,
		}},
		Limit: 20,
	}
	sql, _ := buildSelectQueryFull("posts", qp, tables["posts"], tables)

	// Spread should inline the column, not wrap in JSON.
	if !strings.Contains(sql, "_emb_author.name") {
		t.Errorf("missing inlined spread column in: %s", sql)
	}
	if strings.Contains(sql, "json_build_object") || strings.Contains(sql, "row_to_json") {
		t.Errorf("spread should not use JSON wrapping: %s", sql)
	}
}

func TestBuildSelectQuery_SpreadBelongsToStar(t *testing.T) {
	tables := threeTableFixture()
	// posts → ...author(*)
	qp := &QueryParams{
		Select: []string{"title", "...author(*)"},
		Embeds: []Embed{{
			Name: "author", FKColumn: "author_id", RefTable: "authors", RefColumn: "id",
			Spread: true,
		}},
		Limit: 20,
	}
	sql, _ := buildSelectQueryFull("posts", qp, tables["posts"], tables)

	// All author columns should be inlined.
	if !strings.Contains(sql, "_emb_author.id") || !strings.Contains(sql, "_emb_author.name") {
		t.Errorf("missing inlined spread columns in: %s", sql)
	}
}

func TestBuildSelectQuery_NestedEmbedArgIndexing(t *testing.T) {
	tables := threeTableFixture()
	// authors → posts(title, author(*)) with filter on posts AND outer filter.
	qp := &QueryParams{
		Select: []string{"*", "posts(title,author(name))"},
		Embeds: []Embed{{
			Name: "posts", IsReverse: true,
			FKColumn: "author_id", RefTable: "posts", RefColumn: "id",
			Columns: []string{"title"},
			Children: []Embed{{
				Name: "author", FKColumn: "author_id", RefTable: "authors", RefColumn: "id",
				Columns: []string{"name"},
			}},
			Where: andLeaves(Filter{Column: "status", Operator: "eq", Value: "published"}),
		}},
		Where: andLeaves(Filter{Column: "name", Operator: "eq", Value: "alice"}),
		Limit: 20,
	}
	sql, args := buildSelectQueryFull("authors", qp, tables["authors"], tables)

	// Embed filter should be $1, outer WHERE should be $2.
	if !strings.Contains(sql, "status = $1") {
		t.Errorf("embed filter should be $1, got: %s", sql)
	}
	if !strings.Contains(sql, "name = $2") {
		t.Errorf("outer filter should be $2, got: %s", sql)
	}
	if len(args) != 2 || args[0] != "published" || args[1] != "alice" {
		t.Errorf("args = %v", args)
	}
}

func TestAliasWhereColumns_LeafAndTree(t *testing.T) {
	tree := &WhereNode{
		Op: "or",
		Children: []*WhereNode{
			{Leaf: &Filter{Column: "name", Operator: "eq", Value: "bob"}},
			{Leaf: &Filter{Column: "active", Operator: "eq", Value: "true"}, Not: true},
		},
	}
	got := aliasWhereColumns(tree, "_emb_author")
	if got.Op != "or" || len(got.Children) != 2 {
		t.Fatalf("tree shape changed: %+v", got)
	}
	if got.Children[0].Leaf.Column != "_emb_author.name" {
		t.Errorf("child[0] col = %q", got.Children[0].Leaf.Column)
	}
	if got.Children[1].Leaf.Column != "_emb_author.active" || !got.Children[1].Not {
		t.Errorf("child[1] = %+v", got.Children[1])
	}
	// original tree must be unchanged
	if tree.Children[0].Leaf.Column != "name" {
		t.Error("aliasWhereColumns mutated input")
	}
}

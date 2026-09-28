package postgrest

import (
	"errors"
	"net/url"
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fk(ref string) *domain.ForeignKey { return &domain.ForeignKey{References: ref} }

// m2mTables is posts <- post_tags -> tags, where post_tags has a composite PK.
func m2mTables() map[string]domain.Table {
	return map[string]domain.Table{
		"posts": {Fields: []domain.Field{{Name: "id", PrimaryKey: true}, {Name: "title"}}},
		"tags":  {Fields: []domain.Field{{Name: "id", PrimaryKey: true}, {Name: "name"}}},
		"post_tags": {Fields: []domain.Field{
			{Name: "post_id", PrimaryKey: true, ForeignKey: fk("posts.id")},
			{Name: "tag_id", PrimaryKey: true, ForeignKey: fk("tags.id")},
			{Name: "name"},
		}},
	}
}

func m2mSQL(t *testing.T, tables map[string]domain.Table, parent, sel string, vals url.Values) string {
	t.Helper()
	qp := &QueryParams{Limit: NoLimit}
	qp.Select = ParseSelectParam(sel)
	var embeds []string
	for _, s := range qp.Select {
		if len(s) > 0 && s[len(s)-1] == ')' {
			embeds = append(embeds, s)
		}
	}
	resolved, err := ResolveEmbeds(parent, tables[parent], embeds, tables)
	require.NoError(t, err)
	qp.Embeds = resolved
	byName := map[string]*Embed{}
	for i := range qp.Embeds {
		byName[qp.Embeds[i].OutputKey()] = &qp.Embeds[i]
	}
	require.NoError(t, ParseEmbedScopedParams(vals, byName, tables))
	sql, _ := BuildSelectQueryFull(parent, qp, tables[parent], tables)
	return sql
}

func TestBuildSelect_ManyToManyJoinsThroughJunction(t *testing.T) {
	tables := m2mTables()
	assert.Equal(t,
		"SELECT posts.id, (SELECT coalesce(json_agg(json_build_object('name', tags.name)), '[]'::json) FROM tags, post_tags WHERE tags.id = post_tags.tag_id AND post_tags.post_id = posts.id) AS tags FROM posts",
		m2mSQL(t, tables, "posts", "id,tags(name)", nil))

	assert.Equal(t,
		"SELECT tags.*, (SELECT coalesce(json_agg(row_to_json(posts.*)), '[]'::json) FROM posts, post_tags WHERE posts.id = post_tags.post_id AND post_tags.tag_id = tags.id) AS posts FROM tags",
		m2mSQL(t, tables, "tags", "*,posts(*)", nil), "the reverse direction uses the same junction")
}

func TestBuildSelect_ManyToManyScopedParamsQualifyTarget(t *testing.T) {
	tables := m2mTables()
	vals := url.Values{"tags.name": {"eq.go"}, "tags.order": {"name.desc"}}
	assert.Equal(t,
		"SELECT posts.id, (SELECT coalesce(json_agg(json_build_object('name', tags.name) ORDER BY tags.name DESC), '[]'::json) FROM tags, post_tags WHERE tags.id = post_tags.tag_id AND post_tags.post_id = posts.id AND tags.name = $1) AS tags FROM posts",
		m2mSQL(t, tables, "posts", "id,tags(name)", vals), "junction shares the name column, so target columns must be qualified")

	vals = url.Values{"tags.name": {"eq.go"}, "tags.order": {"name"}, "tags.limit": {"2"}, "tags.offset": {"1"}}
	assert.Equal(t,
		"SELECT posts.id, (SELECT coalesce(json_agg(json_build_object('name', tags.name)), '[]'::json) FROM (SELECT tags.* FROM tags, post_tags WHERE tags.id = post_tags.tag_id AND post_tags.post_id = posts.id AND tags.name = $1 ORDER BY tags.name ASC LIMIT 2 OFFSET 1) tags) AS tags FROM posts",
		m2mSQL(t, tables, "posts", "id,tags(name)", vals), "junction columns must not leak into the paged row")
}

func TestBuildSelect_ManyToManyInnerFiltersParents(t *testing.T) {
	sql := m2mSQL(t, m2mTables(), "posts", "id,tags!inner(name)", url.Values{"tags.name": {"eq.go"}})
	assert.Contains(t, sql, " FROM posts WHERE EXISTS (SELECT 1 FROM tags, post_tags WHERE tags.id = post_tags.tag_id AND post_tags.post_id = posts.id AND tags.name = $2)")
}

func TestBuildSelect_ManyToManyNestedChild(t *testing.T) {
	tables := m2mTables()
	tables["authors"] = domain.Table{Fields: []domain.Field{{Name: "id", PrimaryKey: true}}}
	tables["posts"] = domain.Table{Fields: []domain.Field{
		{Name: "id", PrimaryKey: true}, {Name: "author_id", ForeignKey: fk("authors.id")},
	}}
	sql := m2mSQL(t, tables, "authors", "id,posts(id,tags(name))", url.Values{})
	assert.Contains(t, sql, "'tags', (SELECT coalesce(json_agg(json_build_object('name', tags.name)), '[]'::json) FROM tags, post_tags WHERE tags.id = post_tags.tag_id AND post_tags.post_id = posts.id)")
}

func TestResolveEmbeds_JunctionDetection(t *testing.T) {
	tests := []struct {
		name     string
		junction domain.Table
		want     bool
	}{
		{"composite pk of both fks", m2mTables()["post_tags"], true},
		{"extra pk column", domain.Table{Fields: []domain.Field{
			{Name: "post_id", PrimaryKey: true, ForeignKey: fk("posts.id")},
			{Name: "tag_id", PrimaryKey: true, ForeignKey: fk("tags.id")},
			{Name: "role", PrimaryKey: true},
		}}, true},
		{"schema-qualified references", domain.Table{Fields: []domain.Field{
			{Name: "post_id", PrimaryKey: true, ForeignKey: fk("public.posts.id")},
			{Name: "tag_id", PrimaryKey: true, ForeignKey: fk("public.tags.id")},
		}}, true},
		{"surrogate pk", domain.Table{Fields: []domain.Field{
			{Name: "id", PrimaryKey: true},
			{Name: "post_id", ForeignKey: fk("posts.id")},
			{Name: "tag_id", ForeignKey: fk("tags.id")},
		}}, false},
		{"only one fk in pk", domain.Table{Fields: []domain.Field{
			{Name: "post_id", PrimaryKey: true, ForeignKey: fk("posts.id")},
			{Name: "tag_id", ForeignKey: fk("tags.id")},
		}}, false},
		{"unique leg is one-to-one", domain.Table{Fields: []domain.Field{
			{Name: "post_id", PrimaryKey: true, Unique: true, ForeignKey: fk("posts.id")},
			{Name: "tag_id", PrimaryKey: true, ForeignKey: fk("tags.id")},
		}}, false},
		{"no fields", domain.Table{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tables := m2mTables()
			tables["post_tags"] = tc.junction
			embeds, err := ResolveEmbeds("posts", tables["posts"], []string{"tags(*)"}, tables)
			if !tc.want {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, embeds, 1)
			e := embeds[0]
			assert.True(t, e.IsReverse)
			assert.Equal(t, "tags", e.RefTable)
			assert.Equal(t, &Junction{Table: "post_tags", SourceColumn: "post_id", SourceRef: "id", TargetColumn: "tag_id", TargetRef: "id"}, e.Junction)
		})
	}
}

func TestResolveEmbeds_SelfManyToManyNeverMatches(t *testing.T) {
	tables := map[string]domain.Table{
		"users": {Fields: []domain.Field{{Name: "id", PrimaryKey: true}}},
		"follows": {Fields: []domain.Field{
			{Name: "follower_id", PrimaryKey: true, ForeignKey: fk("users.id")},
			{Name: "followee_id", PrimaryKey: true, ForeignKey: fk("users.id")},
		}},
	}
	_, err := ResolveEmbeds("users", tables["users"], []string{"users(*)"}, tables)
	require.Error(t, err)
}

func TestResolveEmbeds_ManyToManyAmbiguity(t *testing.T) {
	tables := m2mTables()
	tables["post_labels"] = domain.Table{Fields: []domain.Field{
		{Name: "post_id", PrimaryKey: true, ForeignKey: fk("posts.id")},
		{Name: "label_id", PrimaryKey: true, ForeignKey: fk("tags.id")},
	}}

	_, err := ResolveEmbeds("posts", tables["posts"], []string{"tags(*)"}, tables)
	var amb *AmbiguousEmbedError
	require.True(t, errors.As(err, &amb), "got %v", err)
	assert.Equal(t, "Could not embed because more than one relationship was found for 'posts' and 'tags'", amb.Error())
	assert.Equal(t, []map[string]string{
		{"cardinality": "many-to-many", "embedding": "posts with tags", "relationship": "post_labels using post_labels_post_id_fkey(post_id) and post_labels_label_id_fkey(label_id)"},
		{"cardinality": "many-to-many", "embedding": "posts with tags", "relationship": "post_tags using post_tags_post_id_fkey(post_id) and post_tags_tag_id_fkey(tag_id)"},
	}, amb.Details)
	assert.Equal(t, "Try changing 'tags' to one of the following: 'tags!post_labels', 'tags!post_tags'. Find the desired relationship in the 'details' key.", amb.Hint())

	embeds, err := ResolveEmbeds("posts", tables["posts"], []string{"t:tags!post_labels!inner(name)"}, tables)
	require.NoError(t, err)
	require.Len(t, embeds, 1)
	assert.Equal(t, "post_labels", embeds[0].Junction.Table)
	assert.Equal(t, "label_id", embeds[0].Junction.TargetColumn)
	assert.Equal(t, "t", embeds[0].OutputKey())
	assert.True(t, embeds[0].Inner)

	_, err = ResolveEmbeds("posts", tables["posts"], []string{"tags!nope(*)"}, tables)
	require.Error(t, err)
	assert.False(t, errors.As(err, &amb), "an unmatched hint is not-found, not ambiguous")
}

func TestResolveEmbeds_DirectAndManyToManyAmbiguity(t *testing.T) {
	tables := m2mTables()
	tables["posts"] = domain.Table{Fields: []domain.Field{
		{Name: "id", PrimaryKey: true}, {Name: "main_tag_id", ForeignKey: fk("public.tags.id")},
	}}
	tables["tags"] = domain.Table{Fields: []domain.Field{
		{Name: "id", PrimaryKey: true}, {Name: "name"}, {Name: "origin_id", Unique: true, ForeignKey: fk("public.posts.id")},
	}}

	_, err := ResolveEmbeds("posts", tables["posts"], []string{"tags(*)"}, tables)
	var amb *AmbiguousEmbedError
	require.True(t, errors.As(err, &amb), "got %v", err)
	assert.Equal(t, []map[string]string{
		{"cardinality": "many-to-one", "embedding": "posts with tags", "relationship": "posts_main_tag_id_fkey using posts(main_tag_id) and tags(id)"},
		{"cardinality": "one-to-one", "embedding": "posts with tags", "relationship": "tags_origin_id_fkey using posts(id) and tags(origin_id)"},
		{"cardinality": "many-to-many", "embedding": "posts with tags", "relationship": "post_tags using post_tags_post_id_fkey(post_id) and post_tags_tag_id_fkey(tag_id)"},
	}, amb.Details, "PostgREST orders by cardinality (O2M, M2O, O2O, M2M), then name")
	assert.Equal(t, "Try changing 'tags' to one of the following: 'tags!posts_main_tag_id_fkey', 'tags!tags_origin_id_fkey', 'tags!post_tags'. Find the desired relationship in the 'details' key.", amb.Hint())

	embeds, err := ResolveEmbeds("posts", tables["posts"], []string{"tags!posts_main_tag_id_fkey(*)"}, tables)
	require.NoError(t, err, "each hint the error suggests must resolve")
	assert.False(t, embeds[0].IsReverse)
	assert.Equal(t, "main_tag_id", embeds[0].FKColumn)

	embeds, err = ResolveEmbeds("posts", tables["posts"], []string{"tags!tags_origin_id_fkey(*)"}, tables)
	require.NoError(t, err)
	assert.True(t, embeds[0].IsReverse)
	assert.Nil(t, embeds[0].Junction)

	embeds, err = ResolveEmbeds("posts", tables["posts"], []string{"tags!post_tags(*)"}, tables)
	require.NoError(t, err)
	assert.NotNil(t, embeds[0].Junction)
}

func TestResolveEmbeds_ManyToManyRejectsSpreadAndBadColumns(t *testing.T) {
	tables := m2mTables()
	_, err := ResolveEmbeds("posts", tables["posts"], []string{"...tags(name)"}, tables)
	require.ErrorContains(t, err, "spread")
	_, err = ResolveEmbeds("posts", tables["posts"], []string{"tags(post_id)"}, tables)
	require.ErrorContains(t, err, "unknown column", "junction columns are not target columns")
}

// tasksTables has two FKs from tasks to users and none to anything else.
func tasksTables() map[string]domain.Table {
	return map[string]domain.Table{
		"users": {Fields: []domain.Field{{Name: "id", PrimaryKey: true}, {Name: "name"}}},
		"tasks": {Fields: []domain.Field{
			{Name: "id", PrimaryKey: true},
			{Name: "created_by", ForeignKey: fk("users.id")},
			{Name: "assigned_to", ForeignKey: fk("users.id")},
		}},
	}
}

func TestResolveEmbeds_DirectAmbiguity(t *testing.T) {
	tables := tasksTables()
	var amb *AmbiguousEmbedError

	_, err := ResolveEmbeds("tasks", tables["tasks"], []string{"users(*)"}, tables)
	require.True(t, errors.As(err, &amb), "got %v", err)
	assert.Equal(t, []map[string]string{
		{"cardinality": "many-to-one", "embedding": "tasks with users", "relationship": "tasks_assigned_to_fkey using tasks(assigned_to) and users(id)"},
		{"cardinality": "many-to-one", "embedding": "tasks with users", "relationship": "tasks_created_by_fkey using tasks(created_by) and users(id)"},
	}, amb.Details)
	assert.Equal(t, []string{"'users!tasks_assigned_to_fkey'", "'users!tasks_created_by_fkey'"}, amb.Hints)

	_, err = ResolveEmbeds("users", tables["users"], []string{"tasks(*)"}, tables)
	require.True(t, errors.As(err, &amb), "got %v", err)
	assert.Equal(t, []map[string]string{
		{"cardinality": "one-to-many", "embedding": "users with tasks", "relationship": "tasks_assigned_to_fkey using users(id) and tasks(assigned_to)"},
		{"cardinality": "one-to-many", "embedding": "users with tasks", "relationship": "tasks_created_by_fkey using users(id) and tasks(created_by)"},
	}, amb.Details)

	for hint, col := range map[string]string{
		"tasks_assigned_to_fkey": "assigned_to", "tasks_created_by_fkey": "created_by", "assigned_to": "assigned_to", "created_by": "created_by",
	} {
		embeds, err := ResolveEmbeds("tasks", tables["tasks"], []string{"users!" + hint + "(*)"}, tables)
		require.NoError(t, err, hint)
		assert.Equal(t, col, embeds[0].FKColumn, hint)
		embeds, err = ResolveEmbeds("users", tables["users"], []string{"tasks!" + hint + "(*)"}, tables)
		require.NoError(t, err, hint)
		assert.Equal(t, col, embeds[0].FKColumn, hint)
	}

	_, err = ResolveEmbeds("tasks", tables["tasks"], []string{"users!id(*)"}, tables)
	require.True(t, errors.As(err, &amb), "both FKs reference users.id")
}

func TestResolveEmbeds_ReferencedColumnHint(t *testing.T) {
	tables := map[string]domain.Table{
		"authors": {Fields: []domain.Field{{Name: "id", PrimaryKey: true}, {Name: "code", Unique: true}}},
		"posts": {Fields: []domain.Field{
			{Name: "id", PrimaryKey: true},
			{Name: "author_id", ForeignKey: fk("authors.id")},
			{Name: "author_code", ForeignKey: fk("authors.code")},
		}},
	}
	embeds, err := ResolveEmbeds("posts", tables["posts"], []string{"authors!code(*)"}, tables)
	require.NoError(t, err)
	assert.Equal(t, "author_code", embeds[0].FKColumn)

	embeds, err = ResolveEmbeds("authors", tables["authors"], []string{"posts!code(*)"}, tables)
	require.NoError(t, err, "on to-many the parent's referenced column is a hint too")
	assert.Equal(t, "author_code", embeds[0].FKColumn)
	assert.True(t, embeds[0].IsReverse)

	_, err = ResolveEmbeds("posts", tables["posts"], []string{"authors!nope(*)"}, tables)
	require.Error(t, err)
}

func TestResolveEmbeds_SchemaQualifiedReferences(t *testing.T) {
	tables := map[string]domain.Table{
		"tags":  {Fields: []domain.Field{{Name: "id", PrimaryKey: true}}},
		"posts": {Fields: []domain.Field{{Name: "id", PrimaryKey: true}, {Name: "tag_id", ForeignKey: fk("public.tags.id")}, {Name: "user_id", ForeignKey: fk("auth.users.id")}}},
	}
	embeds, err := ResolveEmbeds("posts", tables["posts"], []string{"tags(*)"}, tables)
	require.NoError(t, err)
	assert.Equal(t, Embed{Name: "tags", FKColumn: "tag_id", RefTable: "tags", RefColumn: "id"}, embeds[0])

	embeds, err = ResolveEmbeds("tags", tables["tags"], []string{"posts(*)"}, tables)
	require.NoError(t, err)
	assert.Equal(t, "tag_id", embeds[0].FKColumn)
	assert.True(t, embeds[0].IsReverse)

	_, err = ResolveEmbeds("posts", tables["posts"], []string{"user(*)"}, tables)
	require.Error(t, err, "a table outside the config is not embeddable")
}

func TestToManyScope_QualifiesOnlyJunctionEmbeds(t *testing.T) {
	where := &WhereNode{Leaf: &Filter{Column: "name", Operator: "eq", Value: "x"}}
	order := []OrderClause{{Column: "name"}}
	w, o := ToManyScope(Embed{RefTable: "tags", Where: where, Order: order})
	assert.Same(t, where, w)
	assert.Equal(t, order, o)
	w, o = ToManyScope(Embed{RefTable: "tags", Junction: &Junction{Table: "post_tags"}, Where: where, Order: order})
	assert.Equal(t, "tags.name", w.Leaf.Column)
	assert.Equal(t, "tags.name", o[0].Column)
}

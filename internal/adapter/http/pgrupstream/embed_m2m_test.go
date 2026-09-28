//go:build integration

package pgrupstream

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Post 2's link to sql is hidden from non-bypass roles by RLS on the junction.
const m2mSQL = `
DROP FUNCTION IF EXISTS all_posts();
DROP TABLE IF EXISTS post_labels, post_tags, posts, tags;
CREATE TABLE posts (id int PRIMARY KEY, title text);
CREATE TABLE tags (id int PRIMARY KEY, name text);
CREATE TABLE post_tags (
  post_id int REFERENCES posts(id),
  tag_id  int REFERENCES tags(id),
  visible boolean NOT NULL DEFAULT true,
  PRIMARY KEY (post_id, tag_id)
);
CREATE TABLE post_labels (
  post_id  int REFERENCES posts(id),
  label_id int REFERENCES tags(id),
  PRIMARY KEY (post_id, label_id)
);
INSERT INTO posts VALUES (1, 'a'), (2, 'b'), (3, 'c');
INSERT INTO tags VALUES (1, 'go'), (2, 'sql'), (3, 'rust');
INSERT INTO post_tags VALUES (1, 1, true), (1, 2, true), (2, 2, false), (2, 3, true);
INSERT INTO post_labels VALUES (1, 3);
ALTER TABLE post_tags ENABLE ROW LEVEL SECURITY;
CREATE POLICY post_tags_visible ON post_tags FOR SELECT USING (visible);
CREATE OR REPLACE FUNCTION public.all_posts() RETURNS SETOF posts LANGUAGE sql STABLE AS $$ SELECT * FROM posts $$;
`

func m2mServer(t *testing.T, labels bool) string {
	t.Helper()
	return serverWith(t, func(c *domain.Config) {
		c.Tables["posts"] = domain.Table{Fields: []domain.Field{{Name: "id", Type: "int", PrimaryKey: true}, {Name: "title", Type: "text"}}}
		c.Tables["tags"] = domain.Table{Fields: []domain.Field{{Name: "id", Type: "int", PrimaryKey: true}, {Name: "name", Type: "text"}}}
		c.Tables["post_tags"] = domain.Table{Fields: []domain.Field{
			{Name: "post_id", Type: "int", PrimaryKey: true, ForeignKey: &domain.ForeignKey{References: "posts.id"}},
			{Name: "tag_id", Type: "int", PrimaryKey: true, ForeignKey: &domain.ForeignKey{References: "tags.id"}},
			{Name: "visible", Type: "boolean"},
		}}
		if labels {
			c.Tables["post_labels"] = domain.Table{Fields: []domain.Field{
				{Name: "post_id", Type: "int", PrimaryKey: true, ForeignKey: &domain.ForeignKey{References: "posts.id"}},
				{Name: "label_id", Type: "int", PrimaryKey: true, ForeignKey: &domain.ForeignKey{References: "tags.id"}},
			}}
		}
		c.RPC["all_posts"] = domain.Function{
			Language: "sql", Volatility: "stable", Security: "invoker",
			Returns: domain.FuncReturn{Type: "setof posts"}, ReturnCategory: "setof",
			Body: "SELECT * FROM posts",
		}
	})
}

// tagNames maps post id to its embedded tag names.
func tagNames(t *testing.T, raw []byte, key string) map[float64][]string {
	t.Helper()
	out := map[float64][]string{}
	for _, r := range rowsOf(t, raw) {
		items, ok := r[key].([]any)
		require.True(t, ok, "%s must be an array, got %#v", key, r[key])
		names := []string{}
		for _, it := range items {
			names = append(names, it.(map[string]any)["name"].(string))
		}
		out[r["id"].(float64)] = names
	}
	return out
}

func TestConf_ManyToManyEmbed(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	require.NoError(t, testDB.ExecDDL(context.Background(), m2mSQL))
	base := m2mServer(t, false)
	get := func(t *testing.T, path string, anon bool) []byte {
		t.Helper()
		status, _, raw := call(t, "GET", base+path, "", nil, anon)
		require.Equal(t, 200, status, "%s", raw)
		return raw
	}

	t.Run("basic, with an empty array for a post without tags", func(t *testing.T) {
		raw := get(t, "/rest/v1/posts?select=id,tags(name)&order=id&tags.order=name", false)
		assert.Equal(t, map[float64][]string{1: {"go", "sql"}, 2: {"rust", "sql"}, 3: {}}, tagNames(t, raw, "tags"))
	})

	t.Run("reverse direction and nesting", func(t *testing.T) {
		raw := get(t, "/rest/v1/tags?select=id,name,posts(id)&order=id&posts.order=id", false)
		var rows []struct {
			Name  string
			Posts []struct{ ID int }
		}
		require.NoError(t, json.Unmarshal(raw, &rows))
		require.Len(t, rows, 3)
		assert.Len(t, rows[0].Posts, 1)
		assert.Len(t, rows[1].Posts, 2, "sql links posts 1 and 2")

		raw = get(t, "/rest/v1/posts?select=id,tags(name,posts(id))&id=eq.3", false)
		assert.JSONEq(t, `[{"id":3,"tags":[]}]`, string(raw))
	})

	t.Run("embed filter, order, limit and offset", func(t *testing.T) {
		raw := get(t, "/rest/v1/posts?select=id,tags(name)&order=id&tags.name=eq.sql", false)
		assert.Equal(t, map[float64][]string{1: {"sql"}, 2: {"sql"}, 3: {}}, tagNames(t, raw, "tags"))

		raw = get(t, "/rest/v1/posts?select=id,tags(name)&order=id&tags.order=name.desc&tags.limit=1&tags.offset=1", false)
		assert.Equal(t, map[float64][]string{1: {"go"}, 2: {"rust"}, 3: {}}, tagNames(t, raw, "tags"))
	})

	t.Run("!inner drops posts without a matching tag, and the count agrees", func(t *testing.T) {
		status, hdr, raw := call(t, "GET", base+"/rest/v1/posts?select=id,t:tags!inner(name)&t.name=eq.go", "", map[string]string{"Prefer": "count=exact"}, false)
		require.Equal(t, 200, status, "%s", raw)
		assert.Equal(t, map[float64][]string{1: {"go"}}, tagNames(t, raw, "t"))
		assert.Equal(t, "0-0/1", hdr.Get("Content-Range"))
	})

	t.Run("an aggregate groups by the embed", func(t *testing.T) {
		raw := get(t, "/rest/v1/posts?select=count(),tags(name)&tags.name=eq.go", false)
		counts := map[string]float64{}
		for _, r := range rowsOf(t, raw) {
			b, _ := json.Marshal(r["tags"])
			counts[string(b)] = r["count"].(float64)
		}
		assert.Equal(t, map[string]float64{`[{"name":"go"}]`: 1, `[]`: 2}, counts)
	})

	t.Run("RLS on the junction hides the linked target", func(t *testing.T) {
		raw := get(t, "/rest/v1/posts?select=id,tags(name)&id=eq.2&tags.order=name", true)
		assert.Equal(t, map[float64][]string{2: {"rust"}}, tagNames(t, raw, "tags"), "anon must not see the hidden sql link")
		raw = get(t, "/rest/v1/posts?select=id,tags(name)&id=eq.2&tags.order=name", false)
		assert.Equal(t, map[float64][]string{2: {"rust", "sql"}}, tagNames(t, raw, "tags"), "service_role bypasses RLS")
	})

	t.Run("RPC results embed through the junction", func(t *testing.T) {
		raw := get(t, "/rest/v1/rpc/all_posts?select=id,tags(name)&order=id", false)
		got := tagNames(t, raw, "tags")
		assert.ElementsMatch(t, []string{"go", "sql"}, got[1])
		assert.Equal(t, []string{}, got[3])

		raw = get(t, "/rest/v1/rpc/all_posts?select=id,tags!inner(name)&order=id", true)
		got = tagNames(t, raw, "tags")
		assert.Len(t, got, 2, "post 3 has no tags")
		assert.Equal(t, []string{"rust"}, got[2], "RLS applies on the RPC path too")
	})
}

func TestConf_ManyToManyAmbiguity(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	require.NoError(t, testDB.ExecDDL(context.Background(), m2mSQL))
	base := m2mServer(t, true)

	for _, path := range []string{"/rest/v1/posts?select=id,tags(name)", "/rest/v1/rpc/all_posts?select=id,tags(name)"} {
		status, _, raw := call(t, "GET", base+path, "", nil, false)
		require.Equal(t, 300, status, "%s: %s", path, raw)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		assert.Equal(t, "PGRST201", body["code"])
		assert.Equal(t, "Could not embed because more than one relationship was found for 'posts' and 'tags'", body["message"])
		assert.Equal(t, "Try changing 'tags' to one of the following: 'tags!post_labels', 'tags!post_tags'. Find the desired relationship in the 'details' key.", body["hint"])
		assert.Equal(t, []any{
			map[string]any{"cardinality": "many-to-many", "embedding": "posts with tags", "relationship": "post_labels using post_labels_post_id_fkey(post_id) and post_labels_label_id_fkey(label_id)"},
			map[string]any{"cardinality": "many-to-many", "embedding": "posts with tags", "relationship": "post_tags using post_tags_post_id_fkey(post_id) and post_tags_tag_id_fkey(tag_id)"},
		}, body["details"])
	}

	status, _, raw := call(t, "GET", base+"/rest/v1/posts?select=id,tags!post_labels(name)&order=id", "", nil, false)
	require.Equal(t, 200, status, "%s", raw)
	assert.Equal(t, map[float64][]string{1: {"rust"}, 2: {}, 3: {}}, tagNames(t, raw, "tags"))

	status, _, raw = call(t, "GET", base+"/rest/v1/posts?select=id,tags!post_tags(name)&id=eq.1&tags.order=name", "", nil, false)
	require.Equal(t, 200, status, "%s", raw)
	assert.Equal(t, map[float64][]string{1: {"go", "sql"}}, tagNames(t, raw, "tags"))
}

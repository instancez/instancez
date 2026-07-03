package cli

import (
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/stretchr/testify/assert"
)

func TestRenderConfigDiff_NoChanges(t *testing.T) {
	out := renderConfigDiff(cloud.ConfigDiff{})
	assert.Contains(t, out, "No pending changes")
}

func TestRenderConfigDiff_TableAndColumnAndSection(t *testing.T) {
	diff := cloud.ConfigDiff{
		HasChanges: true,
		Tables: []cloud.TableChange{
			{Name: "todos", Change: cloud.ChangeAdded},
			{Name: "posts", Change: cloud.ChangeModified, Columns: []cloud.ColumnChange{
				{Name: "author_id", Change: cloud.ChangeAdded},
			}},
			{Name: "legacy", Change: cloud.ChangeRemoved},
		},
		Sections: []cloud.SectionChange{
			{Path: "on.user_created", Change: cloud.ChangeAdded},
		},
	}
	out := renderConfigDiff(diff)
	assert.Contains(t, out, "+ table todos")
	assert.Contains(t, out, "~ table posts")
	assert.Contains(t, out, "+ column author_id")
	assert.Contains(t, out, "- table legacy")
	assert.Contains(t, out, "+ config on.user_created")
}

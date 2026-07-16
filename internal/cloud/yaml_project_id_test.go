package cloud

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadProjectID(t *testing.T) {
	src := `version: 1
project:
  name: my app
  cloud:
    project_id: abc-123
tables:
  todos: {}
`
	id, err := ReadProjectID([]byte(src))
	assert.NoError(t, err)
	assert.Equal(t, "abc-123", id)
}

func TestReadProjectIDMissing(t *testing.T) {
	src := `version: 1
project:
  name: my app
`
	id, err := ReadProjectID([]byte(src))
	assert.NoError(t, err)
	assert.Equal(t, "", id)
}

func TestWriteProjectIDNew(t *testing.T) {
	src := `version: 1
project:
  name: my app
tables:
  todos: {}
`
	out, err := WriteProjectID([]byte(src), "abc-123")
	assert.NoError(t, err)
	assert.Contains(t, string(out), "project_id: abc-123")
	// Existing structure is preserved (table todos still there).
	assert.Contains(t, string(out), "todos:")
	// Order preserved: project before tables.
	assert.Less(t, strings.Index(string(out), "project:"), strings.Index(string(out), "tables:"))
}

func TestWriteProjectIDUpdate(t *testing.T) {
	src := `version: 1
project:
  name: my app
  cloud:
    project_id: old-id
`
	out, err := WriteProjectID([]byte(src), "new-id")
	assert.NoError(t, err)
	assert.Contains(t, string(out), "project_id: new-id")
	assert.NotContains(t, string(out), "old-id")
}

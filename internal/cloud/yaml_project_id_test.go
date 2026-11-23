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

// onlyInserted asserts out equals src plus exactly the given added lines.
func onlyInserted(t *testing.T, src, out string, added ...string) {
	t.Helper()
	srcLines := strings.Split(src, "\n")
	outLines := strings.Split(out, "\n")
	assert.Len(t, outLines, len(srcLines)+len(added))
	var rest []string
	extra := map[string]int{}
	for _, a := range added {
		extra[a]++
	}
	for _, l := range outLines {
		if extra[l] > 0 {
			extra[l]--
			continue
		}
		rest = append(rest, l)
	}
	assert.Equal(t, srcLines, rest)
}

func TestWriteProjectIDPreservesFormatAndComments(t *testing.T) {
	src := "# top comment\nversion: 1\nproject:\n    name:   my app   # keep\n    description: \"x\"\n\n\ntables:\n    todos:   {}\n"
	out, err := WriteProjectID([]byte(src), "abc-123")
	assert.NoError(t, err)
	onlyInserted(t, src, string(out), "    cloud:", "        project_id: abc-123")
	id, _ := ReadProjectID(out)
	assert.Equal(t, "abc-123", id)
}

func TestWriteProjectIDNoProjectBlockAppends(t *testing.T) {
	src := "version: 1\ntables:\n  a: {}\n"
	out, err := WriteProjectID([]byte(src), "id1")
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(out), src))
	id, _ := ReadProjectID(out)
	assert.Equal(t, "id1", id)
}

func TestWriteProjectIDNoTrailingNewline(t *testing.T) {
	src := "version: 1\nproject:\n  name: a"
	out, err := WriteProjectID([]byte(src), "id1")
	assert.NoError(t, err)
	id, _ := ReadProjectID(out)
	assert.Equal(t, "id1", id)
	assert.True(t, strings.HasSuffix(string(out), "name: a"))
}

func TestWriteProjectIDExistingCloudBlock(t *testing.T) {
	src := "project:\n  name: a\n  cloud:\n    region: x # c\ntables: {}\n"
	out, err := WriteProjectID([]byte(src), "id1")
	assert.NoError(t, err)
	onlyInserted(t, src, string(out), "    project_id: id1")
}

func TestWriteProjectIDUpdateKeepsRestVerbatim(t *testing.T) {
	src := "project:\n    cloud:\n        project_id: old   # trailing\n    name: a\n"
	out, err := WriteProjectID([]byte(src), "new")
	assert.NoError(t, err)
	assert.Equal(t, "project:\n    cloud:\n        project_id: new   # trailing\n    name: a\n", string(out))
}

func TestWriteProjectIDFlowAndEmptyInputs(t *testing.T) {
	out, err := WriteProjectID([]byte("project: {name: a}\n"), "id1")
	assert.NoError(t, err)
	id, _ := ReadProjectID(out)
	assert.Equal(t, "id1", id)
	_, err = WriteProjectID([]byte(""), "id1")
	assert.Error(t, err)
	_, err = WriteProjectID([]byte("- a\n"), "id1")
	assert.Error(t, err)
}

func TestWriteProjectIDFallsBackWhenInPlaceEditIsUnsafe(t *testing.T) {
	cases := map[string]string{
		"empty value":        "project:\n  cloud:\n    project_id:\n  name: a\n",
		"empty with comment": "project:\n  cloud:\n    project_id: # fill me\n",
		"null value":         "project:\n  cloud:\n    project_id: null\n",
		"flow top level":     "{version: 1}\n",
		"crlf":               "version: 1\r\nproject:\r\n  name: a\r\n",
		"quoted old id":      "project:\n  cloud:\n    project_id: \"old\"\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := WriteProjectID([]byte(src), "new-id")
			assert.NoError(t, err)
			id, err := ReadProjectID(out)
			assert.NoError(t, err)
			assert.Equal(t, "new-id", id)
		})
	}
}

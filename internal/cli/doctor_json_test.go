package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/instancez/instancez/internal/cli/preflight"
)

func TestRenderDoctorJSON(t *testing.T) {
	var buf bytes.Buffer
	err := renderDoctorJSON(&buf, []preflight.Result{
		{Name: "a", OK: true},
		{Name: "b", OK: false, Detail: "failed b", FixHint: "fix b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK     bool                         `json:"ok"`
		Checks []map[string]json.RawMessage `json:"checks"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || len(got.Checks) != 2 {
		t.Fatalf("got %+v", got)
	}
	if _, has := got.Checks[0]["detail"]; has {
		t.Error("check a has detail key")
	}
	if string(got.Checks[1]["hint"]) != `"fix b"` {
		t.Errorf("hint = %s", got.Checks[1]["hint"])
	}
}

func TestRenderDoctorJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderDoctorJSON(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "{\"ok\":true,\"checks\":[]}\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestDoctorJSONStdoutIsPureJSON(t *testing.T) {
	t.Setenv("INSTANCEZ_DATABASE_URL", "")
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	root := NewRootCmd()
	root.SetArgs([]string{"doctor", "--json", "--config", "missing.yaml"})
	root.SetOut(&out)
	execErr := root.Execute()

	if !errors.Is(execErr, errReported) {
		t.Fatalf("err = %v, want errReported", execErr)
	}
	var got struct {
		OK     bool             `json:"ok"`
		Checks []map[string]any `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output not pure JSON: %v\n%s", err, out.Bytes())
	}
	if got.OK || len(got.Checks) == 0 {
		t.Errorf("got %+v", got)
	}
}

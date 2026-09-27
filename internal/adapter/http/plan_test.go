package http

import (
	"reflect"
	"testing"
)

func TestParsePlanAccept(t *testing.T) {
	cases := []struct {
		accept  string
		isPlan  bool
		wantErr bool
		want    planRequest
	}{
		{`application/vnd.pgrst.plan+text; for="application/json"; options=;`, true, false, planRequest{"text", "application/json", nil}},
		{`application/vnd.pgrst.plan+json; for="application/json"; options=analyze|verbose|settings|buffers|wal;`, true, false,
			planRequest{"json", "application/json", []string{"ANALYZE", "VERBOSE", "SETTINGS", "BUFFERS", "WAL"}}},
		{`application/vnd.pgrst.plan+text; for="application/vnd.pgrst.object+json"; options=analyze;`, true, false,
			planRequest{"text", "application/vnd.pgrst.object+json", []string{"ANALYZE"}}},
		{`application/vnd.pgrst.plan`, true, false, planRequest{"text", "application/json", nil}},
		{`  Application/Vnd.Pgrst.Plan+JSON  `, true, false, planRequest{"json", "application/json", nil}},
		{`application/vnd.pgrst.plan+json; for="text/csv; charset=utf-8"`, true, false, planRequest{"json", "text/csv", nil}},
		{`application/vnd.pgrst.plan; FOR=Application/JSON; OPTIONS=Analyze`, true, false, planRequest{"text", "application/json", []string{"ANALYZE"}}},
		{`application/vnd.pgrst.plan; for=""; options=`, true, false, planRequest{"text", "application/json", nil}},
		{`application/vnd.pgrst.plan; options=analyze|analyze|bogus||`, true, false, planRequest{"text", "application/json", []string{"ANALYZE"}}},
		{`application/vnd.pgrst.plan; for="text/xml"`, true, true, planRequest{}},
		{`application/vnd.pgrst.plan+yaml`, false, false, planRequest{}},
		{`application/vnd.pgrst.plan; options=analyze); DROP TABLE x`, true, false, planRequest{"text", "application/json", nil}},
		{`application/vnd.pgrst.plan; for="application/json`, true, false, planRequest{"text", "application/json", nil}},
		{`application/json`, false, false, planRequest{}},
		{`;`, false, false, planRequest{}},
		{``, false, false, planRequest{}},
	}
	for _, c := range cases {
		got, isPlan, err := parsePlanAccept(c.accept)
		if isPlan != c.isPlan || (err != nil) != c.wantErr {
			t.Fatalf("%q: isPlan=%v err=%v", c.accept, isPlan, err)
		}
		if c.isPlan && !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v want %+v", c.accept, got, c.want)
		}
	}
}

func TestPlanRequestExplainSQL(t *testing.T) {
	p := planRequest{"json", "application/json", []string{"ANALYZE", "BUFFERS"}}
	if got := p.explainSQL("SELECT 1"); got != "EXPLAIN (FORMAT JSON, ANALYZE, BUFFERS) SELECT 1" {
		t.Errorf("got %q", got)
	}
	if got := p.contentType(); got != `application/vnd.pgrst.plan+json; for="application/json"; charset=utf-8` {
		t.Errorf("got %q", got)
	}
	if got := (planRequest{format: "text", forType: "application/json"}).explainSQL("SELECT 1"); got != "EXPLAIN (FORMAT TEXT) SELECT 1" {
		t.Errorf("got %q", got)
	}
}

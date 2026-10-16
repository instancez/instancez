package templates

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBundleHasYAMLAndFunctions(t *testing.T) {
	c := mustDefault(t)
	tpl, ok := c.Get("blog")
	if !ok {
		t.Fatal("blog missing")
	}
	b := tpl.Bundle()
	if b.Files["instancez.yaml"] != tpl.YAML {
		t.Error("instancez.yaml must equal the template yaml")
	}
	for p := range tpl.Files {
		if _, ok := b.Files[p]; !ok {
			t.Errorf("function file %s missing from bundle", p)
		}
	}
	if b.Manifest.ID != "blog" {
		t.Errorf("manifest id = %q", b.Manifest.ID)
	}
}

func TestBundleDoesNotAliasTemplateFiles(t *testing.T) {
	tpl, _ := mustDefault(t).Get("course-platform")
	b := tpl.Bundle()
	b.Files["instancez.yaml"] = "mutated"
	if tpl.Bundle().Files["instancez.yaml"] == "mutated" {
		t.Fatal("Bundle must copy the file map")
	}
	if _, polluted := tpl.Files["instancez.yaml"]; polluted {
		t.Fatal("Bundle must not write instancez.yaml into Template.Files")
	}
}

func TestBundleJSONRoundTrip(t *testing.T) {
	tpl, _ := mustDefault(t).Get("course-platform")
	raw, err := json.Marshal(tpl.Bundle())
	if err != nil {
		t.Fatal(err)
	}
	var got Bundle
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Files["instancez.yaml"] != tpl.YAML || len(got.Manifest.Secrets) == 0 {
		t.Fatalf("round trip lost data: %+v", got.Manifest)
	}
	if strings.Contains(string(raw), `"YAML"`) {
		t.Error("raw template fields leaked")
	}
}

func TestLocalYAMLAddsStorageProviderOnlyWithBuckets(t *testing.T) {
	c := mustDefault(t)
	withBuckets, withoutBuckets := 0, 0
	for _, tpl := range c.List() {
		b := tpl.Bundle()
		local := b.LocalYAML()
		if len(tpl.Includes.Buckets) == 0 {
			withoutBuckets++
			if local != tpl.YAML {
				t.Errorf("%s: yaml changed though it has no buckets", tpl.ID)
			}
			continue
		}
		withBuckets++
		if !strings.Contains(local, "providers:\n  storage:\n    type: local") {
			t.Errorf("%s: storage provider not appended", tpl.ID)
		}
		if !strings.HasSuffix(local, "\n") {
			t.Errorf("%s: must end with newline", tpl.ID)
		}
	}
	if withBuckets == 0 || withoutBuckets == 0 {
		t.Fatalf("catalog must cover both cases (buckets=%d none=%d)", withBuckets, withoutBuckets)
	}
}

func TestNormalizeID(t *testing.T) {
	for in, want := range map[string]string{"Blog": "blog", "  AI-Chat\n": "ai-chat", "": "", "x": "x"} {
		if got := NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuggest(t *testing.T) {
	c := mustDefault(t)
	cases := []struct {
		in   string
		want string // first suggestion, "" = none expected
	}{
		{"blgo", "blog"},
		{"kanban", "kanban-board"},
		{"ai-chatt", "ai-chat"},
		{"zzzzzzzz", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := c.Suggest(tc.in)
		if got == nil {
			t.Errorf("Suggest(%q) must never be nil", tc.in)
		}
		if len(got) > 3 {
			t.Errorf("Suggest(%q) returned %d, max 3", tc.in, len(got))
		}
		switch {
		case tc.want == "" && len(got) != 0:
			t.Errorf("Suggest(%q) = %v, want none", tc.in, got)
		case tc.want != "" && (len(got) == 0 || got[0] != tc.want):
			t.Errorf("Suggest(%q) = %v, want first %q", tc.in, got, tc.want)
		}
	}
}

func mustDefault(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(embedded, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

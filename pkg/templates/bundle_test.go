package templates

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
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

func TestSuggestHugeIDIsBounded(t *testing.T) {
	c := mustDefault(t)
	start := time.Now()
	got := c.Suggest(strings.Repeat("a", 1<<20))
	if got == nil || len(got) != 0 {
		t.Fatalf("got %v, want non-nil empty", got)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("took %v", d)
	}
}

func TestSuggestNearMissStillWorks(t *testing.T) {
	if got := mustDefault(t).Suggest("blog2"); len(got) == 0 || got[0] != "blog" {
		t.Fatalf("Suggest(blog2) = %v", got)
	}
}

func TestSuggestCapsAtThreeByDistanceThenAlpha(t *testing.T) {
	c := mustDefault(t)
	const in = "ba"
	type hit struct {
		id string
		d  int
	}
	var all []hit
	for _, tpl := range c.List() {
		if d := editDistance(in, tpl.ID); d <= 3 || strings.Contains(tpl.ID, in) {
			all = append(all, hit{tpl.ID, d})
		}
	}
	if len(all) <= 3 {
		t.Fatalf("query %q matches only %d ids, pick another", in, len(all))
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return all[i].id < all[j].id
	})
	want := []string{all[0].id, all[1].id, all[2].id}
	if got := c.Suggest(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Suggest(%q) = %v, want %v", in, got, want)
	}
}

func TestSafeFilePath(t *testing.T) {
	for _, p := range []string{"instancez.yaml", "functions/a.js", "functions/package.json", "functions/a-b_c.1.js", "functions/console.js"} {
		if !SafeFilePath(p) {
			t.Errorf("%q should be safe", p)
		}
	}
	for _, p := range []string{
		"", "functions/../../evil", "../evil", "/etc/passwd", `functions\evil.js`, "functions/a/b.js",
		"functions/", "functions/.", "functions/..", "functions/.env", ".development.env",
		"functions/a\x00.js", "functions/CON", "functions/nul.js", "functions/com1.txt",
		"functions/a.js.", "INSTANCEZ.yaml", "functions/a b.js",
	} {
		if SafeFilePath(p) {
			t.Errorf("%q should be unsafe", p)
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

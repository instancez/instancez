package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readCapture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "demo", "captures", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDemoYAMLMatchesScaffold(t *testing.T) {
	want := withSchemaHeader(scaffoldYAML("my-app", true))
	if got := readCapture(t, "yaml0.yaml"); got != want {
		t.Fatal("docs/demo/captures/yaml0.yaml drifted from the inz init scaffold; re-record the demo captures (docs/demo/README.md)")
	}
	if !strings.Contains(readCapture(t, "yaml1.yaml"), "- name: priority") {
		t.Fatal("yaml1.yaml lost the priority field")
	}
}

func TestDemoMigrationPlanUsesOwnerScopedAvatars(t *testing.T) {
	plan := readCapture(t, "validate_plan.raw")
	if n := strings.Count(plan, "name LIKE auth.uid()::text || '/%'"); n < 4 {
		t.Fatalf("plan has %d owner-scoped avatars predicates, want at least 4", n)
	}
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, "storage_avatars_") && !strings.Contains(line, "_select_") &&
			strings.Contains(line, "auth.is_authenticated()") {
			t.Fatalf("avatars write policy is not owner-scoped: %s", line)
		}
	}
}

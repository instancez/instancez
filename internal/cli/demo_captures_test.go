package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func readDemoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "demo", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDemoYAMLMatchesScaffold(t *testing.T) {
	want := withSchemaHeader(scaffoldYAML("my-app", true))
	yaml0 := readDemoFile(t, "captures/yaml0.yaml")
	if yaml0 != want {
		t.Fatal("docs/demo/captures/yaml0.yaml drifted from the inz init scaffold; re-record the demo captures (docs/demo/README.md)")
	}

	player := readDemoFile(t, "player.src.html")
	m := regexp.MustCompile(`after: (\d+), lines: \[([^\]]*)\]`).FindStringSubmatch(player)
	if m == nil {
		t.Fatal("player.src.html has no edit step with after/lines")
	}
	after, _ := strconv.Atoi(m[1])
	var added []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[2], -1) {
		added = append(added, q[1])
	}
	lines := strings.Split(yaml0, "\n")
	if after < 1 || after > len(lines) {
		t.Fatalf("edit anchor after: %d is outside yaml0 (%d lines)", after, len(lines))
	}
	edited := append(append(append([]string{}, lines[:after]...), added...), lines[after:]...)
	if got := readDemoFile(t, "captures/yaml1.yaml"); got != strings.Join(edited, "\n") {
		t.Fatal("yaml1.yaml is not yaml0 plus the player's edit; fix the anchor or re-record")
	}
}

func TestDemoMigrationPlanUsesOwnerScopedAvatars(t *testing.T) {
	const owner = "name LIKE auth.uid()::text || '/%'"
	var writes int
	for _, line := range strings.Split(readDemoFile(t, "captures/validate_plan.raw"), "\n") {
		if !strings.HasPrefix(line, "CREATE POLICY storage_avatars_") || strings.Contains(line, "_select_") {
			continue
		}
		writes++
		if !strings.Contains(line, owner) || strings.Contains(line, "auth.is_authenticated()") {
			t.Fatalf("avatars write policy is not owner-scoped: %s", line)
		}
	}
	if writes != 3 {
		t.Fatalf("found %d avatars write policies in validate_plan.raw, want 3", writes)
	}
}

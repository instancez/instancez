package templates

import (
	"maps"
	"sort"
	"strings"
)

// Bundle is the wire shape the cloud API serves and `inz init --template` consumes.
type Bundle struct {
	Manifest Manifest          `json:"manifest"`
	Files    map[string]string `json:"files"`
}

func (t *Template) Bundle() Bundle {
	files := make(map[string]string, len(t.Files)+1)
	maps.Copy(files, t.Files)
	files["instancez.yaml"] = t.YAML
	return Bundle{Manifest: t.Manifest, Files: files}
}

const localStorage = "providers:\n  storage:\n    type: local\n"

// LocalYAML is the config as run on a developer machine: buckets need a storage provider, which the cloud injects itself.
func (b Bundle) LocalYAML() string {
	y := b.Files["instancez.yaml"]
	if len(b.Manifest.Includes.Buckets) == 0 {
		return y
	}
	return strings.TrimRight(y, "\n") + "\n\n" + localStorage
}

func NormalizeID(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Suggest returns up to three ids close to id, nearest first.
func (c *Catalog) Suggest(id string) []string {
	out := []string{}
	if id == "" {
		return out
	}
	type scored struct {
		id string
		d  int
	}
	var hits []scored
	for _, t := range c.list {
		d := editDistance(id, t.ID)
		if d <= 3 || strings.Contains(t.ID, id) || strings.Contains(id, t.ID) {
			hits = append(hits, scored{t.ID, d})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].d != hits[j].d {
			return hits[i].d < hits[j].d
		}
		return hits[i].id < hits[j].id
	})
	for _, h := range hits[:min(3, len(hits))] {
		out = append(out, h.id)
	}
	return out
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

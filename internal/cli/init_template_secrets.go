package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/instancez/instancez/pkg/templates"
)

func sortedSecrets(in []templates.Secret) []templates.Secret {
	out := append([]templates.Secret(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Required && !out[j].Required })
	return out
}

func appendSecrets(existing, templateID string, secrets []templates.Secret) string {
	var missing []templates.Secret
	for _, s := range sortedSecrets(secrets) {
		if !hasEnvKey(existing, s.Name) {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return existing
	}
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n# Secrets for the %s template (the app boots without them)\n", templateID)
	for _, s := range missing {
		fmt.Fprintf(&b, "# %s: %s\n%s=\n", s.Label, s.NeededFor, s.Name)
	}
	return b.String()
}

func hasEnvKey(text, key string) bool {
	for _, line := range strings.Split(text, "\n") {
		if extractEnvKey(line) == key {
			return true
		}
	}
	return false
}

func renderNextSteps(w io.Writer, m templates.Manifest, dir, cwd string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Done! Next steps:")
	if dir != cwd {
		fmt.Fprintf(w, "  cd %s\n", relOr(cwd, dir))
	}
	if len(m.Secrets) > 0 {
		fmt.Fprintf(w, "\nSecrets for %q:\n", m.ID)
		for _, s := range sortedSecrets(m.Secrets) {
			kind := "optional"
			if s.Required {
				kind = "REQUIRED"
			}
			line := fmt.Sprintf("  %-8s  %-40s %s", kind, s.Name, s.NeededFor)
			if s.URL != "" {
				line += "  " + s.URL
			}
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w, "Set them in:")
		fmt.Fprintln(w, "  .development.env   for `inz dev`    (cp .development.env.example .development.env)")
		fmt.Fprintln(w, "  .production.env    for `inz serve`  (cp .production.env.example .production.env)")
		fmt.Fprintln(w, "The app boots without them; functions that need a missing secret return 503 naming it.")
	}
	if len(m.Setup) > 0 {
		fmt.Fprintln(w, "\nManual steps:")
		for i, s := range m.Setup {
			fmt.Fprintf(w, "  %d. %s\n", i+1, s)
		}
	}
	fmt.Fprintln(w, "\n  inz dev --embedded-pg   # no Postgres install needed")
}

func relOr(cwd, dir string) string {
	if r, err := filepath.Rel(cwd, dir); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return dir
}

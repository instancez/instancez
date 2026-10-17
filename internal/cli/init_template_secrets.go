package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/instancez/instancez/pkg/templates"
)

func sortedSecrets(in []templates.Secret) []templates.Secret {
	var out []templates.Secret
	for _, s := range in {
		if templates.ValidSecretName(s.Name) {
			out = append(out, s)
		}
	}
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
	_, _ = fmt.Fprintf(&b, "\n# Secrets for the %s template (the app boots without them)\n", templates.Clean(templateID))
	for _, s := range missing {
		_, _ = fmt.Fprintf(&b, "# %s: %s\n%s=\n", templates.Clean(s.Label), templates.Clean(s.NeededFor), s.Name)
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

func renderNextSteps(w io.Writer, m templates.Manifest, dir string) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Done! Next steps:")
	if dir != "." && dir != "" {
		_, _ = fmt.Fprintf(w, "  cd %s\n", dir)
	}
	if secrets := sortedSecrets(m.Secrets); len(secrets) > 0 {
		_, _ = fmt.Fprintf(w, "\nSecrets for %q:\n", templates.Clean(m.ID))
		for _, s := range secrets {
			kind := "optional"
			if s.Required {
				kind = "REQUIRED"
			}
			line := fmt.Sprintf("  %-8s  %-40s %s", kind, s.Name, templates.Clean(s.NeededFor))
			if u := templates.Clean(s.URL); u != "" {
				line += "  " + u
			}
			_, _ = fmt.Fprintln(w, line)
		}
		_, _ = fmt.Fprintln(w, "Set them in:")
		_, _ = fmt.Fprintln(w, "  .development.env   for `inz dev`    (cp .development.env.example .development.env)")
		_, _ = fmt.Fprintln(w, "  .production.env    for `inz serve`  (cp .production.env.example .production.env)")
		_, _ = fmt.Fprintln(w, "The app boots without them; functions that need a missing secret return 503 naming it.")
	}
	if len(m.Setup) > 0 {
		_, _ = fmt.Fprintln(w, "\nManual steps:")
		for i, s := range m.Setup {
			_, _ = fmt.Fprintf(w, "  %d. %s\n", i+1, templates.Clean(s))
		}
	}
	_, _ = fmt.Fprintln(w, "\n  inz dev --embedded-pg   # no Postgres install needed")
}

package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/instancez/instancez/internal/vet"
	"github.com/mattn/go-isatty"
)

const wrapWidth = 88

// useColor is true only for a terminal with NO_COLOR unset.
func useColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

type painter bool

func (p painter) wrap(code, s string) string {
	if !p {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func sevCode(s vet.Severity) string {
	switch s {
	case vet.Critical:
		return "1;31"
	case vet.High:
		return "31"
	case vet.Medium:
		return "33"
	case vet.Low:
		return "34"
	}
	return "2"
}

// wrapText breaks s at spaces so lines stay within width runes.
func wrapText(s string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		if cur != "" && utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur != "" {
			cur += " "
		}
		cur += w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func writeWrapped(w io.Writer, prefix, hang, text string) {
	for i, l := range wrapText(text, wrapWidth-len(hang)) {
		p := hang
		if i == 0 {
			p = prefix
		}
		fmt.Fprintf(w, "%s%s\n", p, l)
	}
}

func renderVetReport(w io.Writer, r *vet.Report, file string, color bool) {
	p := painter(color)
	fmt.Fprintf(w, "%s  %s\n", p.wrap("1", "inz vet"), p.wrap("2", file))

	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "\n %s\n", p.wrap("32", "✓ No security findings"))
		writeChecks(w, p, r.Checks)
		return
	}

	for sev := vet.Critical; sev >= vet.Info; sev-- {
		for _, f := range r.Findings {
			if f.Severity != sev {
				continue
			}
			code := sevCode(sev)
			name := strings.ToUpper(sev.String())
			loc := file
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", file, f.Line)
			}
			fmt.Fprintf(w, "\n %s %s  %s %s\n", p.wrap(code, "●"), p.wrap(code, fmt.Sprintf("%-8s", name)),
				p.wrap("1", fmt.Sprintf("%-32s", f.Rule)), p.wrap("2", loc))
			if f.Path != "" {
				fmt.Fprintf(w, "   %s\n", p.wrap("2", f.Path))
			}
			writeWrapped(w, "   ", "   ", f.Message)
			if f.Fix != "" {
				writeWrapped(w, "   Fix: ", "        ", f.Fix)
			}
		}
	}

	parts := make([]string, 0, 5)
	for sev := vet.Critical; sev >= vet.Info; sev-- {
		n := r.Counts[sev.String()]
		s := fmt.Sprintf("%d %s", n, sev)
		if n == 0 {
			parts = append(parts, p.wrap("2", s))
		} else {
			parts = append(parts, p.wrap(sevCode(sev), s))
		}
	}
	fmt.Fprintf(w, "\n %s\n", strings.Join(parts, p.wrap("2", " · ")))
	writeChecks(w, p, r.Checks)
}

func writeChecks(w io.Writer, p painter, c vet.Checks) {
	if c.Total > 0 {
		fmt.Fprintf(w, " %s\n", p.wrap("2", fmt.Sprintf("%d of %d checks passed", c.Passed, c.Total)))
	}
}

func renderVetVerdict(w io.Writer, failed bool, failOn vet.Severity, color bool) {
	p := painter(color)
	if failed {
		fmt.Fprintf(w, " %s\n", p.wrap("1;31", fmt.Sprintf("✗ failed: findings at or above %s", failOn)))
		return
	}
	fmt.Fprintf(w, " %s\n", p.wrap("32", "✓ passed"))
}

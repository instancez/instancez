package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/instancez/instancez/internal/vet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reportOf(fs ...vet.Finding) *vet.Report {
	r := &vet.Report{Findings: fs, Counts: map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0}}
	for _, f := range fs {
		r.Counts[f.Severity.String()]++
	}
	return r
}

func render(r *vet.Report, color bool) string {
	var b bytes.Buffer
	renderVetReport(&b, r, "instancez.yaml", color)
	return b.String()
}

var sample = vet.Finding{
	Rule: "policy-open-write", Severity: vet.Critical, Path: "tables.posts.rls[2].with_check", Line: 42,
	Message: "Anyone can insert rows into posts.", Fix: "Require auth.uid().",
}

func TestRenderPlainHasNoANSI(t *testing.T) {
	out := render(reportOf(sample), false)
	assert.NotContains(t, out, "\x1b[")
	assert.Contains(t, out, "● CRITICAL  policy-open-write")
	assert.Contains(t, out, "instancez.yaml:42")
	assert.Contains(t, out, "   tables.posts.rls[2].with_check\n")
	assert.Contains(t, out, "   Fix: Require auth.uid().")
	assert.Contains(t, out, "1 critical · 0 high · 0 medium · 0 low · 0 info")
}

func TestRenderColorEmitsANSI(t *testing.T) {
	out := render(reportOf(sample), true)
	assert.Contains(t, out, "\x1b[1;31m")
	assert.Contains(t, out, "\x1b[2m0 high\x1b[0m", "zero counts are dim")
}

func TestRenderEmpty(t *testing.T) {
	out := render(reportOf(), false)
	assert.Contains(t, out, "✓ No security findings")
	assert.NotContains(t, out, "critical")
}

func TestRenderChecksFooter(t *testing.T) {
	r := reportOf(sample)
	r.Checks = vet.Checks{Total: 21, Passed: 20}
	assert.Contains(t, render(r, false), "\n 20 of 21 checks passed\n")
	assert.Contains(t, render(r, true), "20 of 21 checks passed")
	clean := reportOf()
	clean.Checks = vet.Checks{Total: 21, Passed: 21}
	assert.Contains(t, render(clean, false), "21 of 21 checks passed")
}

func TestRenderChecksFooterOmittedWhenTotalZero(t *testing.T) {
	assert.NotContains(t, render(reportOf(sample), false), "checks passed")
	assert.NotContains(t, render(reportOf(), false), "checks passed")
}

func TestRenderLineZeroOmitsLine(t *testing.T) {
	f := sample
	f.Line = 0
	out := render(reportOf(f), false)
	assert.NotContains(t, out, "instancez.yaml:")
	assert.Contains(t, out, "policy-open-write")
}

func TestRenderOrdersBySeverityDescending(t *testing.T) {
	lo := vet.Finding{Rule: "r-low", Severity: vet.Low, Message: "m"}
	hi := vet.Finding{Rule: "r-high", Severity: vet.High, Message: "m"}
	out := render(reportOf(lo, hi, sample), false)
	assert.Less(t, strings.Index(out, "policy-open-write"), strings.Index(out, "r-high"))
	assert.Less(t, strings.Index(out, "r-high"), strings.Index(out, "r-low"))
}

func TestRenderWrapsWithHangingIndent(t *testing.T) {
	f := sample
	f.Message = strings.Repeat("word ", 60)
	f.Fix = strings.Repeat("fix ", 40)
	out := render(reportOf(f), false)
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "   ") && !strings.Contains(l, "instancez.yaml") {
			assert.LessOrEqual(t, utf8.RuneCountInString(l), wrapWidth, l)
		}
	}
	assert.Contains(t, out, "\n        fix ", "fix continuation lines hang under the text")
}

func TestWrapTextEdges(t *testing.T) {
	assert.Empty(t, wrapText("", 10))
	assert.Empty(t, wrapText("   ", 10))
	long := strings.Repeat("x", 30)
	assert.Equal(t, []string{long}, wrapText(long, 10), "an overlong word is kept whole")
	assert.Equal(t, []string{"héllo wörld", "ünï"}, wrapText("héllo wörld ünï", 11), "counts runes not bytes")
}

func TestRenderUnicodeSafe(t *testing.T) {
	f := sample
	f.Message = strings.Repeat("日本語のメッセージ ", 15)
	out := render(reportOf(f), false)
	assert.True(t, utf8.ValidString(out))
}

func TestVerdict(t *testing.T) {
	var b bytes.Buffer
	renderVetVerdict(&b, true, vet.High, false)
	assert.Contains(t, b.String(), "✗ failed: findings at or above high")
	b.Reset()
	renderVetVerdict(&b, false, vet.High, false)
	assert.Contains(t, b.String(), "✓ passed")
}

func TestUseColor(t *testing.T) {
	assert.False(t, useColor(&bytes.Buffer{}), "non-file writer")
	f, err := os.CreateTemp(t.TempDir(), "x")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	assert.False(t, useColor(f), "regular file is not a tty")
	t.Setenv("NO_COLOR", "1")
	assert.False(t, useColor(os.Stdout))
}

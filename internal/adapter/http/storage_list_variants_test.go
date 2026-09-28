package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCaseVariantRanges(t *testing.T) {
	assert.Equal(t, []string{""}, caseVariantRanges(""))
	assert.Equal(t, []string{"/1"}, caseVariantRanges("/1"), "caseless runes have one variant")
	assert.ElementsMatch(t, []string{"ab", "aB", "Ab", "AB"}, caseVariantRanges("ab"))
	assert.Contains(t, caseVariantRanges("k"), "K", "the Kelvin sign lowers to k")
	assert.Contains(t, caseVariantRanges("é"), "É")

	assert.ElementsMatch(t, []string{"i", "I", "İ"}, caseVariantRanges("i"), "glibc lower() maps U+0130 to i")
	assert.ElementsMatch(t, []string{"i", "I", "İ"}, caseVariantRanges("I"))
	assert.ElementsMatch(t, []string{"İ", "i", "I"}, caseVariantRanges("İ"), "a dotted capital I in the prefix matches names lowered to i")
	assert.Len(t, caseVariantRanges("users/"), 72, "u, s and s (each with the long s), e, r")
	assert.Len(t, caseVariantRanges("users/a"), 144, "the next letter would pass the cap")

	long := caseVariantRanges("abcdefghijklmnop")
	assert.LessOrEqual(t, len(long), maxVariantRanges)
	assert.Len(t, long[0], 8, "runes past the cap are left to the filter")
	assert.NotEmpty(t, caseVariantRanges("\xff"), "invalid utf8 still yields a range")
}

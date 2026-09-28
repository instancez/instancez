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

	long := caseVariantRanges("abcdefghijklmnop")
	assert.LessOrEqual(t, len(long), maxVariantRanges)
	assert.Len(t, long[0], 6, "runes past the cap are left to the filter")
	assert.NotEmpty(t, caseVariantRanges("\xff"), "invalid utf8 still yields a range")
}

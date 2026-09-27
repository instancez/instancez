package http

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngWithDims forges a valid IHDR claiming w x h so DecodeConfig sees huge dimensions cheaply.
func pngWithDims(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	b := buf.Bytes()
	binary.BigEndian.PutUint32(b[16:], w)
	binary.BigEndian.PutUint32(b[20:], h)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29]))
	return b
}

func realPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))))
	return buf.Bytes()
}

func TestParseTransformParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parse := func(q string) (*transformParams, error) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/x?"+q, nil)
		return parseTransformParams(c)
	}
	p, err := parse("")
	assert.NoError(t, err)
	assert.Nil(t, p)
	p, err = parse("width=0&height=0")
	assert.NoError(t, err)
	assert.Nil(t, p)
	p, err = parse("width=abc")
	assert.NoError(t, err)
	assert.Nil(t, p)
	p, err = parse("width=NaN")
	assert.NoError(t, err)
	assert.Nil(t, p)
	p, err = parse("width=1e3")
	assert.NoError(t, err)
	assert.Nil(t, p)
	for _, q := range []string{"width=-1", "height=-5", "width=10&height=-1", "width=-99999999999999999999"} {
		_, err = parse(q)
		assert.ErrorIs(t, err, errInvalidTransform, q)
	}
	p, err = parse("width=99999&height=2501&quality=0")
	require.NoError(t, err)
	assert.Equal(t, 2500, p.Width)
	assert.Equal(t, 2500, p.Height)
	assert.Equal(t, 80, p.Quality)
	p, err = parse("width=2500")
	require.NoError(t, err)
	assert.Equal(t, 2500, p.Width)
	assert.Equal(t, 0, p.Height)
	p, err = parse("width=99999999999999999999")
	require.NoError(t, err)
	assert.Equal(t, 2500, p.Width)
}

func TestApplyTransform_RejectsHugePixelCounts(t *testing.T) {
	for _, d := range [][2]uint32{{10000, 10000}, {7072, 7072}, {65535, 65535}, {1, 50_000_001}} {
		_, _, err := applyTransform(io.NopCloser(bytes.NewReader(pngWithDims(t, d[0], d[1]))), "image/png",
			&transformParams{Width: 10, Height: 10, Resize: "cover", Quality: 80, Format: "png"})
		assert.ErrorIs(t, err, errImageTooLarge, "%dx%d", d[0], d[1])
	}
}

func TestApplyTransform_RejectsOversizedSource(t *testing.T) {
	big := io.NopCloser(io.LimitReader(zeroReader{}, maxTransformBytes+1))
	_, _, err := applyTransform(big, "image/png", &transformParams{Width: 10, Format: "png", Quality: 80})
	assert.ErrorIs(t, err, errImageTooLarge)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestApplyTransform_ClampsImplicitDimension(t *testing.T) {
	out, ct, err := applyTransform(io.NopCloser(bytes.NewReader(realPNG(t, 1, 3000))), "image/png",
		&transformParams{Width: 10, Resize: "fill", Quality: 80, Format: "png"})
	require.NoError(t, err)
	assert.Equal(t, "image/png", ct)
	img, err := png.Decode(out)
	require.NoError(t, err)
	assert.Equal(t, 10, img.Bounds().Dx())
	assert.LessOrEqual(t, img.Bounds().Dy(), maxTransformDim)
}

func TestApplyTransform_UndecodableReturnsOriginal(t *testing.T) {
	out, ct, err := applyTransform(io.NopCloser(bytes.NewReader([]byte("not an image"))), "image/webp",
		&transformParams{Width: 10, Format: "origin", Quality: 80})
	require.NoError(t, err)
	b, _ := io.ReadAll(out)
	assert.Equal(t, "not an image", string(b))
	assert.Equal(t, "image/webp", ct)
}

func assertCoverBounded(t *testing.T, src []byte, params *transformParams) {
	t.Helper()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out, _, err := applyTransform(io.NopCloser(bytes.NewReader(src)), "image/png", params)
	require.NoError(t, err)
	runtime.ReadMemStats(&after)
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(100<<20),
		"cover transform allocated too much for an extreme-aspect source")
	img, err := png.Decode(out)
	require.NoError(t, err)
	assert.Equal(t, maxTransformDim, img.Bounds().Dx())
	assert.Equal(t, maxTransformDim, img.Bounds().Dy())
}

func TestApplyTransform_CoverNarrowTallSourceStaysBounded(t *testing.T) {
	assertCoverBounded(t, realPNG(t, 1, 200_000),
		&transformParams{Width: 2500, Resize: "cover", Quality: 80, Format: "png"})
}

func TestApplyTransform_CoverWideShortSourceStaysBounded(t *testing.T) {
	assertCoverBounded(t, realPNG(t, 200_000, 1),
		&transformParams{Height: 2500, Resize: "cover", Quality: 80, Format: "png"})
}

func TestApplyTransform_CoverNormalOutputDimensionsUnchanged(t *testing.T) {
	out, _, err := applyTransform(io.NopCloser(bytes.NewReader(realPNG(t, 400, 300))), "image/png",
		&transformParams{Width: 150, Height: 150, Resize: "cover", Quality: 80, Format: "png"})
	require.NoError(t, err)
	img, err := png.Decode(out)
	require.NoError(t, err)
	assert.Equal(t, 150, img.Bounds().Dx())
	assert.Equal(t, 150, img.Bounds().Dy())
}

func TestApplyTransform_UnsupportedFormatErrors(t *testing.T) {
	_, _, err := applyTransform(io.NopCloser(bytes.NewReader(realPNG(t, 2, 2))), "image/png",
		&transformParams{Width: 1, Height: 1, Format: "webp", Quality: 80})
	assert.Error(t, err)
	assert.False(t, errors.Is(err, errImageTooLarge))
}

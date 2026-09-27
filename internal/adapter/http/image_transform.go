package http

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
)

const (
	maxTransformDim    = 2500
	maxTransformBytes  = 25 << 20
	maxTransformPixels = 50_000_000
)

var (
	errImageTooLarge    = errors.New("image exceeds 25MB or 50 megapixels")
	errInvalidTransform = errors.New("width and height must be non-negative")
)

type transformParams struct {
	Width   int
	Height  int
	Resize  string // cover, contain, fill
	Quality int    // 1-100
	Format  string // origin, png, jpg/jpeg
}

func parseTransformParams(c *gin.Context) (*transformParams, error) {
	w, _ := strconv.Atoi(c.Query("width"))
	h, _ := strconv.Atoi(c.Query("height"))
	if w < 0 || h < 0 {
		return nil, errInvalidTransform
	}
	if w == 0 && h == 0 {
		return nil, nil
	}
	q, _ := strconv.Atoi(c.Query("quality"))
	if q <= 0 || q > 100 {
		q = 80
	}
	return &transformParams{
		Width:   min(w, maxTransformDim),
		Height:  min(h, maxTransformDim),
		Resize:  c.DefaultQuery("resize", "cover"),
		Quality: q,
		Format:  c.DefaultQuery("format", "origin"),
	}, nil
}

// coverResize crops before resizing so an extreme aspect ratio can't blow up the intermediate image.
func coverResize(img image.Image, width, height int, filter imaging.ResampleFilter) *image.NRGBA {
	srcBounds := img.Bounds()
	srcW, srcH := srcBounds.Dx(), srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 || width <= 0 || height <= 0 {
		return &image.NRGBA{}
	}
	if srcW == width && srcH == height {
		return imaging.Clone(img)
	}

	srcAspectRatio := float64(srcW) / float64(srcH)
	dstAspectRatio := float64(width) / float64(height)

	var cropped *image.NRGBA
	if srcAspectRatio < dstAspectRatio {
		cropH := float64(srcW) * float64(height) / float64(width)
		cropped = imaging.CropAnchor(img, srcW, int(math.Max(1, cropH)+0.5), imaging.Center)
	} else {
		cropW := float64(srcH) * float64(width) / float64(height)
		cropped = imaging.CropAnchor(img, int(math.Max(1, cropW)+0.5), srcH, imaging.Center)
	}
	return imaging.Resize(cropped, width, height, filter)
}

func applyTransform(reader io.ReadCloser, contentType string, params *transformParams) (io.ReadCloser, string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxTransformBytes+1))
	_ = reader.Close()
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxTransformBytes {
		return nil, "", errImageTooLarge
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return io.NopCloser(bytes.NewReader(data)), contentType, nil
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxTransformPixels {
		return nil, "", errImageTooLarge
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return io.NopCloser(bytes.NewReader(data)), contentType, nil
	}

	w, h := params.Width, params.Height
	if w == 0 {
		w = img.Bounds().Dx()
	}
	if h == 0 {
		h = img.Bounds().Dy()
	}
	w, h = min(w, maxTransformDim), min(h, maxTransformDim)

	var result *image.NRGBA
	switch params.Resize {
	case "contain":
		result = imaging.Fit(img, w, h, imaging.Lanczos)
	case "fill":
		result = imaging.Resize(img, w, h, imaging.Lanczos)
	default: // cover
		result = coverResize(img, w, h, imaging.Lanczos)
	}

	outFormat := params.Format
	if outFormat == "origin" || outFormat == "" {
		if strings.Contains(contentType, "png") {
			outFormat = "png"
		} else {
			outFormat = "jpeg"
		}
	}

	var buf bytes.Buffer
	switch strings.ToLower(outFormat) {
	case "png":
		err = png.Encode(&buf, result)
		contentType = "image/png"
	case "jpg", "jpeg":
		err = jpeg.Encode(&buf, result, &jpeg.Options{Quality: params.Quality})
		contentType = "image/jpeg"
	default:
		return nil, "", fmt.Errorf("unsupported output format: %s", outFormat)
	}
	if err != nil {
		return nil, "", err
	}

	return io.NopCloser(&buf), contentType, nil
}

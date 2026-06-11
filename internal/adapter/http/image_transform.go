package http

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
)

type transformParams struct {
	Width   int
	Height  int
	Resize  string // cover, contain, fill
	Quality int    // 1-100
	Format  string // origin, png, jpg/jpeg
}

func parseTransformParams(c *gin.Context) *transformParams {
	w, _ := strconv.Atoi(c.Query("width"))
	h, _ := strconv.Atoi(c.Query("height"))
	if w == 0 && h == 0 {
		return nil
	}
	q, _ := strconv.Atoi(c.Query("quality"))
	if q <= 0 || q > 100 {
		q = 80
	}
	resize := c.DefaultQuery("resize", "cover")
	format := c.DefaultQuery("format", "origin")
	return &transformParams{
		Width:   w,
		Height:  h,
		Resize:  resize,
		Quality: q,
		Format:  format,
	}
}

func applyTransform(reader io.ReadCloser, contentType string, params *transformParams) (io.ReadCloser, string, error) {
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return nil, "", err
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

	var result *image.NRGBA
	switch params.Resize {
	case "contain":
		result = imaging.Fit(img, w, h, imaging.Lanczos)
	case "fill":
		result = imaging.Resize(img, w, h, imaging.Lanczos)
	default: // cover
		result = imaging.Fill(img, w, h, imaging.Center, imaging.Lanczos)
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

package imageproc

import (
	"bytes"
	"context"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/bkincz/reverb/storage"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type Size struct {
	Name  string
	Width int
}

type Processor struct {
	Sizes []Size
}

// ---------------------------------------------------------------------------
// Default
// ---------------------------------------------------------------------------

func Default() *Processor {
	return &Processor{Sizes: []Size{
		{Name: "thumb", Width: 150},
		{Name: "sm", Width: 400},
		{Name: "md", Width: 800},
		{Name: "lg", Width: 1200},
	}}
}

// ---------------------------------------------------------------------------
// ProcessImage
// ---------------------------------------------------------------------------

func (p *Processor) ProcessImage(_ context.Context, original []byte, mime string) ([]storage.ProcessedVariant, int, int, error) {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/png", "image/gif":
	default:
		return nil, 0, 0, nil
	}

	src, _, err := image.Decode(bytes.NewReader(original))
	if err != nil {
		return nil, 0, 0, err
	}

	origBounds := src.Bounds()
	origWidth := origBounds.Dx()
	origHeight := origBounds.Dy()

	var variants []storage.ProcessedVariant

	for _, sz := range p.Sizes {
		if origWidth <= sz.Width {
			continue
		}

		targetWidth := sz.Width
		targetHeight := origHeight * targetWidth / origWidth
		if targetHeight < 1 {
			targetHeight = 1
		}

		dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
		xdraw.BiLinear.Scale(dst, dst.Bounds(), src, origBounds, xdraw.Over, nil)

		data, contentType, encErr := encode(dst, mime)
		if encErr != nil {
			return nil, 0, 0, encErr
		}

		variants = append(variants, storage.ProcessedVariant{
			Name:        sz.Name,
			Data:        data,
			Width:       targetWidth,
			Height:      targetHeight,
			ContentType: contentType,
		})
	}

	return variants, origWidth, origHeight, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func encode(img image.Image, origMime string) ([]byte, string, error) {
	var buf bytes.Buffer

	switch strings.ToLower(origMime) {
	case "image/jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil

	case "image/gif":
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil

	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	}
}

var _ storage.ImageProcessor = (*Processor)(nil)
var _ = gif.GIF{}

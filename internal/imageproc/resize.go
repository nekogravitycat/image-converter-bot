package imageproc

import (
	"fmt"
	"math"

	"github.com/davidbyttow/govips/v2/vips"
)

// FitWithin scales w×h to fit inside maxW×maxH, preserving aspect ratio and never upscaling.
func FitWithin(w, h, maxW, maxH int) (int, int) {
	scale := math.Min(math.Min(float64(maxW)/float64(w), float64(maxH)/float64(h)), 1)
	return scaleDims(w, h, scale)
}

func scaleDims(w, h int, scale float64) (int, int) {
	if scale >= 1 {
		return w, h
	}
	return max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
}

// resizeTo returns a new image of exactly w×h. The source is left untouched so
// repeated size-control attempts always resample from full quality.
func resizeTo(src *vips.ImageRef, w, h int) (*vips.ImageRef, error) {
	out, err := src.Copy()
	if err != nil {
		return nil, fmt.Errorf("copy image: %w", err)
	}
	if w == src.Width() && h == src.Height() {
		return out, nil
	}

	// Premultiply so transparent pixels do not bleed dark fringes into edges.
	if err := out.PremultiplyAlpha(); err != nil {
		out.Close()
		return nil, fmt.Errorf("premultiply: %w", err)
	}
	hScale := float64(w) / float64(src.Width())
	vScale := float64(h) / float64(src.Height())
	if err := out.ResizeWithVScale(hScale, vScale, vips.KernelLanczos3); err != nil {
		out.Close()
		return nil, fmt.Errorf("resize: %w", err)
	}
	if err := out.UnpremultiplyAlpha(); err != nil {
		out.Close()
		return nil, fmt.Errorf("unpremultiply: %w", err)
	}
	return out, nil
}

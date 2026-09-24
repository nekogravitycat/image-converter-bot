package imageproc

import (
	"context"
	"fmt"
	"math"

	"github.com/davidbyttow/govips/v2/vips"
)

type encoded struct {
	data          []byte
	format        Format
	width, height int
}

// encodeWithinLimits resizes to the configured bounds and encodes. With a file-size limit it
// first lowers JPEG quality (or switches opaque PNG to JPEG), then shrinks dimensions and retries.
func encodeWithinLimits(ctx context.Context, base *vips.ImageRef, format Format, keepAlpha bool, opts Options) (*encoded, error) {
	limit := opts.MaxFileSizeBytes
	w, h := FitWithin(base.Width(), base.Height(), opts.MaxWidth, opts.MaxHeight)

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		img, err := resizeTo(base, w, h)
		if err != nil {
			return nil, err
		}
		data, usedFormat, err := encodeOnce(ctx, img, format, keepAlpha, opts)
		img.Close()
		if err != nil {
			return nil, err
		}
		if limit <= 0 || int64(len(data)) <= limit {
			return &encoded{data: data, format: usedFormat, width: w, height: h}, nil
		}
		// Once PNG has been abandoned for size, stay on JPEG while shrinking.
		format = usedFormat

		if attempt >= maxResizeAttempts {
			return nil, fmt.Errorf("%w: still %d bytes after %d attempts", ErrFileSizeLimit, len(data), attempt)
		}
		// File size scales roughly with pixel count, i.e. with the square of the linear factor.
		factor := math.Sqrt(float64(limit)/float64(len(data))) * 0.95
		factor = math.Max(minShrink, math.Min(maxShrink, factor))
		nw, nh := scaleDims(w, h, factor)
		if max(nw, nh) < MinimumDimension {
			return nil, fmt.Errorf("%w: %d bytes at %d×%d, minimum dimension reached", ErrFileSizeLimit, len(data), w, h)
		}
		w, h = nw, nh
	}
}

// encodeOnce encodes img at its current size, applying the quality search for JPEG.
func encodeOnce(ctx context.Context, img *vips.ImageRef, format Format, keepAlpha bool, opts Options) ([]byte, Format, error) {
	limit := opts.MaxFileSizeBytes
	if format == FormatPNG {
		data, err := exportPNG(ctx, img, opts.StripMetadata)
		if err != nil || limit <= 0 || int64(len(data)) <= limit || keepAlpha {
			return data, FormatPNG, err
		}
		// No transparency to protect, so a lossy JPEG is an acceptable way to meet the limit.
	}
	data, err := exportJPEGWithin(ctx, img, opts.JPEGQuality, limit, opts.StripMetadata)
	return data, FormatJPEG, err
}

// exportJPEGWithin returns the highest-quality JPEG in [MinJPEGQuality, quality] that fits limit,
// found by binary search. If even MinJPEGQuality is too big it returns that (oversized) result.
func exportJPEGWithin(ctx context.Context, img *vips.ImageRef, quality int, limit int64, strip bool) ([]byte, error) {
	data, err := exportJPEG(ctx, img, quality, strip)
	if err != nil || limit <= 0 || int64(len(data)) <= limit || quality <= MinJPEGQuality {
		return data, err
	}

	best, err := exportJPEG(ctx, img, MinJPEGQuality, strip)
	if err != nil || int64(len(best)) > limit {
		return best, err
	}
	lo, hi := MinJPEGQuality+1, quality-1
	for lo <= hi {
		mid := (lo + hi) / 2
		candidate, err := exportJPEG(ctx, img, mid, strip)
		if err != nil {
			return nil, err
		}
		if int64(len(candidate)) <= limit {
			best, lo = candidate, mid+1
		} else {
			hi = mid - 1
		}
	}
	return best, nil
}

func exportJPEG(ctx context.Context, img *vips.ImageRef, quality int, strip bool) ([]byte, error) {
	return exportCancellable(ctx, img, "jpeg", func() ([]byte, *vips.ImageMetadata, error) {
		return img.ExportJpeg(&vips.JpegExportParams{
			Quality:        quality,
			StripMetadata:  strip,
			Interlace:      false, // baseline JPEG for maximum viewer compatibility
			OptimizeCoding: true,
			SubsampleMode:  vips.VipsForeignSubsampleAuto,
		})
	})
}

func exportPNG(ctx context.Context, img *vips.ImageRef, strip bool) ([]byte, error) {
	return exportCancellable(ctx, img, "png", func() ([]byte, *vips.ImageMetadata, error) {
		return img.ExportPng(&vips.PngExportParams{
			StripMetadata: strip,
			Compression:   6,
			Filter:        vips.PngFilterAll,
		})
	})
}

// exportCancellable runs an export (where libvips actually evaluates the pipeline) and
// sets the libvips kill flag if ctx ends, so a runaway image cannot hold a worker forever.
func exportCancellable(ctx context.Context, img *vips.ImageRef, name string, export func() ([]byte, *vips.ImageMetadata, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { img.SetKill(true) })
	defer stop()

	data, _, err := export()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: encode %s: %w", ErrDecode, name, err)
	}
	return data, nil
}

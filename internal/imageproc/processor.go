// Package imageproc converts arbitrary input images into VRChat-friendly JPEG or PNG files using libvips.
package imageproc

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidbyttow/govips/v2/vips"
)

// User-facing failure classes. Wrapped errors carry the internal detail for logs.
var (
	ErrUnsupportedFormat = errors.New("unsupported image format")
	ErrDecode            = errors.New("failed to decode image")
	ErrTooLarge          = errors.New("image too large to process safely")
	ErrFileSizeLimit     = errors.New("unable to meet file-size limit")
)

// Format is an output format.
type Format string

// Supported output formats.
const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
)

// Ext returns the file extension including the dot.
func (f Format) Ext() string {
	if f == FormatPNG {
		return ".png"
	}
	return ".jpg"
}

// Options are the per-guild conversion settings.
type Options struct {
	MaxWidth         int
	MaxHeight        int
	MaxFileSizeBytes int64 // 0 disables the limit
	JPEGQuality      int
	PreserveAlpha    bool
	StripMetadata    bool
}

// Result describes a converted image. Input dimensions are after EXIF autorotation.
type Result struct {
	Data         []byte
	Format       Format
	InputFormat  string
	InputWidth   int
	InputHeight  int
	OutputWidth  int
	OutputHeight int
}

// Processor runs the conversion pipeline. It holds no per-image state and is safe for concurrent use.
type Processor struct {
	maxInputPixels int64
}

// NewProcessor returns a Processor that rejects inputs larger than maxInputPixels.
func NewProcessor(maxInputPixels int64) *Processor {
	return &Processor{maxInputPixels: maxInputPixels}
}

// Process runs: decode → autorotate → validate → sRGB → choose format → (flatten) → resize → encode → size control.
func (p *Processor) Process(ctx context.Context, data []byte, opts Options) (*Result, error) {
	img, inputFormat, err := p.decode(data)
	if err != nil {
		return nil, err
	}
	defer img.Close()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inW, inH := img.Width(), img.Height()

	if err := normalizeColour(img); err != nil {
		return nil, fmt.Errorf("%w: normalise colour: %w", ErrDecode, err)
	}

	keepAlpha := img.HasAlpha() && opts.PreserveAlpha
	format := chooseFormat(inputFormat, img.HasAlpha(), opts.PreserveAlpha)
	if img.HasAlpha() && !keepAlpha {
		if err := img.Flatten(&vips.Color{R: 255, G: 255, B: 255}); err != nil {
			return nil, fmt.Errorf("flatten alpha: %w", err)
		}
	}

	enc, err := encodeWithinLimits(ctx, img, format, keepAlpha, opts)
	if err != nil {
		return nil, err
	}
	return &Result{
		Data:         enc.data,
		Format:       enc.format,
		InputFormat:  vips.ImageTypes[inputFormat],
		InputWidth:   inW,
		InputHeight:  inH,
		OutputWidth:  enc.width,
		OutputHeight: enc.height,
	}, nil
}

// chooseFormat implements the output-format policy: alpha kept → PNG, lossless-origin → PNG, else JPEG.
func chooseFormat(input vips.ImageType, hasAlpha, preserveAlpha bool) Format {
	if hasAlpha {
		if preserveAlpha {
			return FormatPNG
		}
		return FormatJPEG
	}
	switch input {
	case vips.ImageTypePNG, vips.ImageTypeGIF:
		return FormatPNG
	default:
		return FormatJPEG
	}
}

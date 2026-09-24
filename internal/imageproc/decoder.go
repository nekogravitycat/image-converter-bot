package imageproc

import (
	"fmt"

	"github.com/davidbyttow/govips/v2/vips"
)

// allowedInputs is the set of formats we hand to libvips. The format is sniffed from the
// bytes, never from the filename or MIME type; SVG, PDF and ImageMagick loaders are excluded.
var allowedInputs = map[vips.ImageType]bool{
	vips.ImageTypeJPEG: true,
	vips.ImageTypePNG:  true,
	vips.ImageTypeWEBP: true,
	vips.ImageTypeHEIF: true,
	vips.ImageTypeAVIF: true,
	vips.ImageTypeGIF:  true,
	vips.ImageTypeTIFF: true,
}

// decode opens the image lazily, rejects oversized inputs before any pixels are decoded,
// then applies EXIF orientation so all later dimension math uses the displayed size.
func (p *Processor) decode(data []byte) (*vips.ImageRef, vips.ImageType, error) {
	format := vips.DetermineImageType(data)
	if !allowedInputs[format] {
		return nil, format, ErrUnsupportedFormat
	}

	// Default import params load only the first frame of animated images.
	img, err := vips.LoadImageFromBuffer(data, vips.NewImportParams())
	if err != nil {
		return nil, format, fmt.Errorf("%w: %w", ErrDecode, err)
	}

	w, h := img.Width(), img.Height()
	if pixels := int64(w) * int64(h); pixels > p.maxInputPixels || pixels <= 0 {
		img.Close()
		return nil, format, fmt.Errorf("%w: %d×%d exceeds %d pixels", ErrTooLarge, w, h, p.maxInputPixels)
	}

	if err := img.AutoRotate(); err != nil {
		img.Close()
		return nil, format, fmt.Errorf("%w: autorotate: %w", ErrDecode, err)
	}
	return img, format, nil
}

// normalizeColour converts to 8-bit sRGB, honouring an embedded ICC profile (e.g. Display P3 or CMYK).
func normalizeColour(img *vips.ImageRef) error {
	if img.HasICCProfile() {
		if err := img.TransformICCProfile(vips.SRGBIEC6196621ICCProfilePath); err != nil {
			// A broken profile should not fail the whole conversion; fall back to a plain colourspace change.
			if err := img.RemoveICCProfile(); err != nil {
				return fmt.Errorf("remove ICC profile: %w", err)
			}
		}
	}
	if img.Interpretation() != vips.InterpretationSRGB {
		if err := img.ToColorSpace(vips.InterpretationSRGB); err != nil {
			return fmt.Errorf("convert to sRGB: %w", err)
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if err := img.Cast(vips.BandFormatUchar); err != nil {
			return fmt.Errorf("cast to 8-bit: %w", err)
		}
	}
	return nil
}

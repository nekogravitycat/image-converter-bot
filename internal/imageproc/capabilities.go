package imageproc

import (
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/davidbyttow/govips/v2/vips"
)

// selfcheckHEIC is a tiny HEVC-coded HEIC. Decoding it proves libheif *and* an HEVC
// decoder plugin are present, which a loader-registry check alone cannot show.
//
//go:embed selfcheck.heic
var selfcheckHEIC []byte

// Startup initialises libvips and routes its log output to slog.
func Startup(logger *slog.Logger) error {
	vips.LoggingSettings(func(domain string, level vips.LogLevel, msg string) {
		switch level {
		case vips.LogLevelError, vips.LogLevelCritical:
			logger.Error(msg, slog.String("domain", domain))
		case vips.LogLevelWarning:
			logger.Warn(msg, slog.String("domain", domain))
		default:
			logger.Debug(msg, slog.String("domain", domain))
		}
	}, vips.LogLevelWarning)

	// Every input is unique, so the operation cache would only hold memory.
	if err := vips.Startup(&vips.Config{ConcurrencyLevel: 1, MaxCacheMem: 0, MaxCacheSize: 0, MaxCacheFiles: 0}); err != nil {
		return fmt.Errorf("start libvips: %w", err)
	}
	return nil
}

// Shutdown releases libvips resources.
func Shutdown() { vips.Shutdown() }

// VipsVersion reports the linked libvips version.
func VipsVersion() string { return vips.Version }

// CheckCapabilities verifies that every format required by the spec can actually be decoded and encoded.
func CheckCapabilities() error {
	for _, t := range []vips.ImageType{vips.ImageTypeJPEG, vips.ImageTypePNG, vips.ImageTypeWEBP, vips.ImageTypeHEIF} {
		if !vips.IsTypeSupported(t) {
			return fmt.Errorf("libvips has no %s loader", vips.ImageTypes[t])
		}
	}

	heic, err := vips.NewImageFromBuffer(selfcheckHEIC)
	if err != nil {
		return fmt.Errorf("HEIC decode: %w", err)
	}
	defer heic.Close()
	// Pixels are decoded lazily; exporting forces a real HEVC decode.
	pixels, _, err := heic.ExportPng(vips.NewPngExportParams())
	if err != nil {
		return fmt.Errorf("HEIC decode (is an HEVC decoder plugin such as libde265 installed?): %w", err)
	}
	src, err := vips.NewImageFromBuffer(pixels)
	if err != nil {
		return fmt.Errorf("PNG decode: %w", err)
	}
	defer src.Close()

	encoders := map[string]func() ([]byte, *vips.ImageMetadata, error){
		"JPEG": func() ([]byte, *vips.ImageMetadata, error) { return src.ExportJpeg(vips.NewJpegExportParams()) },
		"PNG":  func() ([]byte, *vips.ImageMetadata, error) { return src.ExportPng(vips.NewPngExportParams()) },
		"WebP": func() ([]byte, *vips.ImageMetadata, error) { return src.ExportWebp(vips.NewWebpExportParams()) },
	}
	for name, encode := range encoders {
		buf, _, err := encode()
		if err != nil {
			return fmt.Errorf("%s encode: %w", name, err)
		}
		round, err := vips.NewImageFromBuffer(buf)
		if err != nil {
			return fmt.Errorf("%s decode: %w", name, err)
		}
		_, _, err = round.ExportPng(vips.NewPngExportParams())
		round.Close()
		if err != nil {
			return fmt.Errorf("%s decode: %w", name, err)
		}
	}
	return nil
}

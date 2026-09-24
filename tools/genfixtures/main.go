// Command genfixtures writes the synthetic image fixtures used by internal/imageproc tests.
// Run it through tools/genfixtures/generate.sh, which also adds EXIF tags with exiftool.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/davidbyttow/govips/v2/vips"
)

const outDir = "internal/imageproc/testdata"

type rgba struct{ r, g, b, a byte }

var (
	red         = rgba{220, 30, 30, 255}
	blue        = rgba{30, 60, 200, 255}
	green       = rgba{40, 180, 70, 255}
	transparent = rgba{0, 0, 0, 0}
)

// canvas returns w×h pixels coloured by paint(x, y).
func canvas(w, h, bands int, paint func(x, y int) rgba) *vips.ImageRef {
	pix := make([]byte, 0, w*h*bands)
	for y := range h {
		for x := range w {
			c := paint(x, y)
			pix = append(pix, c.r, c.g, c.b)
			if bands == 4 {
				pix = append(pix, c.a)
			}
		}
	}
	img, err := vips.NewImageFromMemory(pix, w, h, bands, vips.BandFormatUchar, vips.InterpretationSRGB)
	if err != nil {
		log.Fatal(err)
	}
	return img
}

// quadrants gives each image some structure so encoders produce realistic output.
func quadrants(w, h int) func(x, y int) rgba {
	return func(x, y int) rgba {
		switch {
		case x < w/2 && y < h/2:
			return red
		case x >= w/2 && y < h/2:
			return green
		default:
			return rgba{byte(x * 255 / w), byte(y * 255 / h), 128, 255}
		}
	}
}

func must(data []byte, _ *vips.ImageMetadata, err error) []byte {
	if err != nil {
		log.Fatal(err)
	}
	return data
}

func write(name string, data []byte) {
	path := filepath.Join(outDir, name)
	if name == "selfcheck.heic" {
		path = filepath.Join("internal/imageproc", name)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", path, len(data))
}

func main() {
	if err := vips.Startup(nil); err != nil {
		log.Fatal(err)
	}
	defer vips.Shutdown()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	jpeg := &vips.JpegExportParams{Quality: 90}
	png := vips.NewPngExportParams()

	write("opaque.jpg", must(canvas(800, 600, 3, quadrants(800, 600)).ExportJpeg(jpeg)))
	write("gps.jpg", must(canvas(320, 240, 3, quadrants(320, 240)).ExportJpeg(jpeg)))

	// Stored pixel matrix is landscape 400×300 with a red marker in the top-left corner.
	// generate.sh tags it Orientation=6 (rotate 90° CW), so it displays as portrait 300×400
	// with the marker in the top-right corner.
	write("orientation6.jpg", must(canvas(400, 300, 3, func(x, y int) rgba {
		if x < 100 && y < 75 {
			return red
		}
		return blue
	}).ExportJpeg(jpeg)))

	write("opaque.png", must(canvas(640, 480, 3, quadrants(640, 480)).ExportPng(png)))
	// Left half fully transparent, right half opaque green.
	alpha := func(x, _ int) rgba {
		if x < 320 {
			return transparent
		}
		return green
	}
	write("alpha.png", must(canvas(640, 480, 4, alpha).ExportPng(png)))

	write("opaque.webp", must(canvas(640, 480, 3, quadrants(640, 480)).ExportWebp(&vips.WebpExportParams{Quality: 90})))
	write("alpha.webp", must(canvas(640, 480, 4, alpha).ExportWebp(&vips.WebpExportParams{Lossless: true})))

	// Portrait phone-like photo: 1512×2016 scales to 1125×1500 under the default 1500 box.
	write("photo.heic", must(canvas(1512, 2016, 3, quadrants(1512, 2016)).ExportHeif(&vips.HeifExportParams{Quality: 60})))
	write("selfcheck.heic", must(canvas(64, 48, 3, quadrants(64, 48)).ExportHeif(&vips.HeifExportParams{Quality: 50})))
}

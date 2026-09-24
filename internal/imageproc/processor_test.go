package imageproc

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidbyttow/govips/v2/vips"
)

func TestMain(m *testing.M) {
	if err := Startup(slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		panic(err)
	}
	code := m.Run()
	Shutdown()
	os.Exit(code)
}

func defaultOpts() Options {
	return Options{MaxWidth: 1500, MaxHeight: 1500, JPEGQuality: 90, PreserveAlpha: true, StripMetadata: true}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func process(t *testing.T, data []byte, opts Options) *Result {
	t.Helper()
	res, err := NewProcessor(100_000_000).Process(context.Background(), data, opts)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return res
}

// decodeOutput re-opens the encoded result and checks it matches what Result claims.
func decodeOutput(t *testing.T, res *Result) *vips.ImageRef {
	t.Helper()
	img, err := vips.NewImageFromBuffer(res.Data)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	t.Cleanup(img.Close)
	wantType := map[Format]vips.ImageType{FormatJPEG: vips.ImageTypeJPEG, FormatPNG: vips.ImageTypePNG}[res.Format]
	if img.Format() != wantType {
		t.Fatalf("output bytes are %v, Result says %s", img.Format(), res.Format)
	}
	if img.Width() != res.OutputWidth || img.Height() != res.OutputHeight {
		t.Fatalf("output is %d×%d, Result says %d×%d", img.Width(), img.Height(), res.OutputWidth, res.OutputHeight)
	}
	return img
}

func TestFitWithin(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{3000, 2000, 1500, 1000},
		{1000, 3000, 500, 1500},
		{1200, 800, 1200, 800},
		{1500, 1500, 1500, 1500},
		{3024, 4032, 1125, 1500},
		{10000, 3, 1500, 1}, // never collapses to zero
	}
	for _, c := range cases {
		if w, h := FitWithin(c.w, c.h, 1500, 1500); w != c.wantW || h != c.wantH {
			t.Errorf("FitWithin(%d×%d) = %d×%d, want %d×%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

func TestFormats(t *testing.T) {
	noAlpha := defaultOpts()
	noAlpha.PreserveAlpha = false
	cases := []struct {
		fixture   string
		opts      Options
		want      Format
		wantW     int
		wantH     int
		inputFmt  string
		wantAlpha bool
	}{
		{"opaque.jpg", defaultOpts(), FormatJPEG, 800, 600, "jpeg", false},
		{"opaque.png", defaultOpts(), FormatPNG, 640, 480, "png", false},
		{"alpha.png", defaultOpts(), FormatPNG, 640, 480, "png", true},
		{"alpha.png", noAlpha, FormatJPEG, 640, 480, "png", false},
		{"opaque.webp", defaultOpts(), FormatJPEG, 640, 480, "webp", false},
		{"alpha.webp", defaultOpts(), FormatPNG, 640, 480, "webp", true},
		{"photo.heic", defaultOpts(), FormatJPEG, 1125, 1500, "heif", false},
	}
	for _, c := range cases {
		t.Run(c.fixture+"/alpha="+map[bool]string{true: "keep", false: "drop"}[c.opts.PreserveAlpha], func(t *testing.T) {
			res := process(t, fixture(t, c.fixture), c.opts)
			if res.Format != c.want || res.OutputWidth != c.wantW || res.OutputHeight != c.wantH {
				t.Fatalf("got %s %d×%d, want %s %d×%d", res.Format, res.OutputWidth, res.OutputHeight, c.want, c.wantW, c.wantH)
			}
			if res.InputFormat != c.inputFmt {
				t.Errorf("input format = %q, want %q", res.InputFormat, c.inputFmt)
			}
			img := decodeOutput(t, res)
			if img.HasAlpha() != c.wantAlpha {
				t.Errorf("output alpha = %v, want %v", img.HasAlpha(), c.wantAlpha)
			}
		})
	}
}

func TestFlattenUsesWhiteBackground(t *testing.T) {
	opts := defaultOpts()
	opts.PreserveAlpha = false
	img := decodeOutput(t, process(t, fixture(t, "alpha.png"), opts))
	px, err := img.GetPoint(10, 10) // inside the transparent half
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range px[:3] {
		if v < 245 {
			t.Fatalf("transparent area flattened to %v, want white", px)
		}
	}
}

func TestOrientationApplied(t *testing.T) {
	res := process(t, fixture(t, "orientation6.jpg"), defaultOpts())
	if res.InputWidth != 300 || res.InputHeight != 400 {
		t.Fatalf("input dims after autorotate = %d×%d, want 300×400", res.InputWidth, res.InputHeight)
	}
	img := decodeOutput(t, res)
	if img.Width() != 300 || img.Height() != 400 {
		t.Fatalf("output %d×%d, want 300×400", img.Width(), img.Height())
	}
	if o := img.Orientation(); o > 1 {
		t.Fatalf("output still carries EXIF orientation %d", o)
	}
	// The red marker was stored top-left; rotating 90° CW puts it top-right.
	isRed := func(x, y int) bool {
		px, err := img.GetPoint(x, y)
		if err != nil {
			t.Fatal(err)
		}
		return px[0] > 150 && px[1] < 100 && px[2] < 100
	}
	if !isRed(290, 10) {
		t.Error("expected red marker at top-right")
	}
	if isRed(10, 10) || isRed(10, 390) || isRed(290, 390) {
		t.Error("red marker found in wrong corner")
	}
}

func TestOrientationKeptMetadataIsNormalised(t *testing.T) {
	opts := defaultOpts()
	opts.StripMetadata = false
	img := decodeOutput(t, process(t, fixture(t, "orientation6.jpg"), opts))
	if o := img.Orientation(); o > 1 {
		t.Fatalf("output with metadata kept still has orientation %d", o)
	}
}

func hasGPS(img *vips.ImageRef) bool {
	for _, f := range img.ImageFields() {
		if strings.HasPrefix(f, "exif-ifd3-") { // IFD3 is the GPS IFD in libvips naming
			return true
		}
	}
	return false
}

func TestStripMetadata(t *testing.T) {
	src, err := vips.NewImageFromBuffer(fixture(t, "gps.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if !hasGPS(src) {
		t.Fatal("fixture gps.jpg has no GPS metadata; regenerate fixtures")
	}

	stripped := decodeOutput(t, process(t, fixture(t, "gps.jpg"), defaultOpts()))
	if hasGPS(stripped) || stripped.HasExif() {
		t.Fatalf("metadata survived stripping: %v", stripped.ImageFields())
	}
	if bytes.Contains(process(t, fixture(t, "gps.jpg"), defaultOpts()).Data, []byte("FixtureCam")) {
		t.Fatal("camera make found in stripped output")
	}

	opts := defaultOpts()
	opts.StripMetadata = false
	if kept := decodeOutput(t, process(t, fixture(t, "gps.jpg"), opts)); !hasGPS(kept) {
		t.Fatal("GPS metadata removed although strip_metadata=false")
	}
}

// noisePNG returns incompressible content so size limits actually bite.
func noisePNG(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if alpha {
				a = uint8(r.IntN(256))
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), a})
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func noiseJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img, err := vips.NewImageFromBuffer(noisePNG(t, w, h, false))
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	data, _, err := img.ExportJpeg(&vips.JpegExportParams{Quality: 95})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFileSizeQualityReduction(t *testing.T) {
	data := noiseJPEG(t, 800, 800)
	unlimited := process(t, data, defaultOpts())

	opts := defaultOpts()
	opts.MaxFileSizeBytes = int64(len(unlimited.Data)) * 3 / 4
	res := process(t, data, opts)
	if int64(len(res.Data)) > opts.MaxFileSizeBytes {
		t.Fatalf("output %d bytes exceeds limit %d", len(res.Data), opts.MaxFileSizeBytes)
	}
	if res.OutputWidth != 800 || res.OutputHeight != 800 {
		t.Fatalf("dimensions changed to %d×%d although quality reduction should suffice", res.OutputWidth, res.OutputHeight)
	}
	decodeOutput(t, res)
}

func TestFileSizeDimensionReduction(t *testing.T) {
	opts := defaultOpts()
	opts.MaxFileSizeBytes = 60 << 10
	res := process(t, noiseJPEG(t, 1500, 1000), opts)
	if int64(len(res.Data)) > opts.MaxFileSizeBytes {
		t.Fatalf("output %d bytes exceeds limit %d", len(res.Data), opts.MaxFileSizeBytes)
	}
	if res.OutputWidth >= 1500 {
		t.Fatalf("expected dimensions to shrink, got %d×%d", res.OutputWidth, res.OutputHeight)
	}
	// Aspect ratio survives the shrink loop (within rounding).
	if ratio := float64(res.OutputWidth) / float64(res.OutputHeight); ratio < 1.49 || ratio > 1.51 {
		t.Fatalf("aspect ratio drifted to %.3f", ratio)
	}
	decodeOutput(t, res)
}

func TestFileSizeOpaquePNGFallsBackToJPEG(t *testing.T) {
	data := noisePNG(t, 600, 600, false)
	opts := defaultOpts()
	opts.MaxFileSizeBytes = 200 << 10
	res := process(t, data, opts)
	if res.Format != FormatJPEG || int64(len(res.Data)) > opts.MaxFileSizeBytes {
		t.Fatalf("got %s, %d bytes; want JPEG within %d", res.Format, len(res.Data), opts.MaxFileSizeBytes)
	}
}

func TestFileSizeAlphaPNGShrinks(t *testing.T) {
	data := noisePNG(t, 600, 600, true)
	opts := defaultOpts()
	opts.MaxFileSizeBytes = 300 << 10
	res := process(t, data, opts)
	if res.Format != FormatPNG {
		t.Fatalf("format = %s, want PNG to keep transparency", res.Format)
	}
	if int64(len(res.Data)) > opts.MaxFileSizeBytes || res.OutputWidth >= 600 {
		t.Fatalf("got %d bytes at %d×%d; want ≤ %d and smaller dimensions", len(res.Data), res.OutputWidth, res.OutputHeight, opts.MaxFileSizeBytes)
	}
	if !decodeOutput(t, res).HasAlpha() {
		t.Fatal("alpha lost")
	}
}

func TestFileSizeImpossible(t *testing.T) {
	opts := defaultOpts()
	opts.MaxFileSizeBytes = 100
	_, err := NewProcessor(100_000_000).Process(context.Background(), noiseJPEG(t, 400, 400), opts)
	if !errors.Is(err, ErrFileSizeLimit) {
		t.Fatalf("err = %v, want ErrFileSizeLimit", err)
	}
}

func TestRejectsUnsupportedAndCorrupt(t *testing.T) {
	p := NewProcessor(100_000_000)
	ctx := context.Background()

	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`)
	for name, data := range map[string][]byte{"text": []byte("definitely not an image at all"), "svg": svg} {
		if _, err := p.Process(ctx, data, defaultOpts()); !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("%s: err = %v, want ErrUnsupportedFormat", name, err)
		}
	}

	jpeg := fixture(t, "opaque.jpg")
	if _, err := p.Process(ctx, jpeg[:len(jpeg)/3], defaultOpts()); !errors.Is(err, ErrDecode) {
		t.Errorf("truncated JPEG: err = %v, want ErrDecode", err)
	}
}

func TestRejectsTooManyPixels(t *testing.T) {
	_, err := NewProcessor(1000).Process(context.Background(), fixture(t, "opaque.jpg"), defaultOpts())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewProcessor(100_000_000).Process(ctx, fixture(t, "opaque.jpg"), defaultOpts())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCheckCapabilities(t *testing.T) {
	if err := CheckCapabilities(); err != nil {
		t.Fatal(err)
	}
}

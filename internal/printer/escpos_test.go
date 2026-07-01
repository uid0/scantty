package printer

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// grayPNG builds a w×h 8-bit grayscale PNG whose pixel luminance is set
// by lum(x, y) and returns the encoded bytes. Encoding is lossless, so
// the encoder under test sees exactly the luminance values we specify.
func grayPNG(t *testing.T, w, h int, lum func(x, y int) uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetGray(x, y, color.Gray{Y: lum(x, y)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode fixture png: %v", err)
	}
	return buf.Bytes()
}

// checker returns a lum func for a black/white checkerboard: a cell is
// black (luminance 0) when x+y is even, white (255) otherwise.
func checker(x, y int) uint8 {
	if (x+y)%2 == 0 {
		return 0
	}
	return 255
}

func TestEncodeCheckerboardExactBytes(t *testing.T) {
	pngData := grayPNG(t, 8, 2, checker)

	var buf bytes.Buffer
	if err := (ESCPOS{WidthDots: 576, Cut: true}).Encode(&buf, pngData); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// ESC @  |  GS v 0 m=0 xL=1 xH=0 yL=2 yH=0  |  data  |  3×LF  |  cut
	// Row 0 (BWBWBWBW) = 0xAA, row 1 (WBWBWBWB) = 0x55, MSB = leftmost.
	want := []byte{
		0x1b, 0x40, // ESC @
		0x1d, 0x76, 0x30, 0x00, 0x01, 0x00, 0x02, 0x00, // GS v 0 header
		0xaa, 0x55, // bitmap
		0x0a, 0x0a, 0x0a, // feed
		0x1d, 0x56, 0x42, 0x00, // GS V 66 0 partial cut
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("byte mismatch:\n got % x\nwant % x", buf.Bytes(), want)
	}
}

func TestEncodeNoCutOmitsCutCommand(t *testing.T) {
	pngData := grayPNG(t, 8, 1, func(x, y int) uint8 { return 0 }) // all black

	var buf bytes.Buffer
	if err := (ESCPOS{WidthDots: 576, Cut: false}).Encode(&buf, pngData); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Feed still runs, but no partial-cut command tails the job.
	if bytes.Contains(buf.Bytes(), []byte(partialCut)) {
		t.Fatalf("Cut=false must not emit partial cut; got % x", buf.Bytes())
	}
	if got, want := buf.Bytes()[len(buf.Bytes())-feedLines:], []byte{0x0a, 0x0a, 0x0a}; !bytes.Equal(got, want) {
		t.Fatalf("job should end with %d line feeds; got % x", feedLines, got)
	}
}

func TestEncodeWidthPaddedToByte(t *testing.T) {
	// 5 dots wide → 1 byte/row, pad bits (x=5..7) must stay blank.
	pngData := grayPNG(t, 5, 1, func(x, y int) uint8 { return 0 }) // all black

	var buf bytes.Buffer
	if err := (ESCPOS{WidthDots: 576}).Encode(&buf, pngData); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// ESC @ (2) + header (8), then the single data byte.
	got := buf.Bytes()
	if xL := got[6]; xL != 0x01 {
		t.Fatalf("xL (bytes/row) = %#x, want 0x01", xL)
	}
	if dataByte := got[10]; dataByte != 0xf8 {
		t.Fatalf("row byte = %#08b, want 0b11111000 (five dots + three blank pad bits)", dataByte)
	}
}

func TestEncodeLuminanceThreshold(t *testing.T) {
	// Left pixel just below the threshold (black dot), right pixel at the
	// threshold (blank) — proves the boundary is exactly < monoThreshold.
	pngData := grayPNG(t, 2, 1, func(x, y int) uint8 {
		if x == 0 {
			return monoThreshold - 1
		}
		return monoThreshold
	})

	var buf bytes.Buffer
	if err := (ESCPOS{WidthDots: 576}).Encode(&buf, pngData); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if dataByte := buf.Bytes()[10]; dataByte != 0x80 {
		t.Fatalf("row byte = %#08b, want 0b10000000 (only the sub-threshold pixel dotted)", dataByte)
	}
}

func TestEncodeChunksTallImageIntoBands(t *testing.T) {
	const rows = 300 // > maxBandRows(255) → two bands: 255 + 45
	pngData := grayPNG(t, 8, rows, func(x, y int) uint8 { return 0 })

	var buf bytes.Buffer
	if err := (ESCPOS{WidthDots: 576}).Encode(&buf, pngData); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()

	// Band 1 header: yL=0xFF yH=0x00 (255 rows) directly after ESC @.
	band1 := []byte{0x1d, 0x76, 0x30, 0x00, 0x01, 0x00, 0xff, 0x00}
	if !bytes.Equal(out[2:2+len(band1)], band1) {
		t.Fatalf("band 1 header = % x, want % x", out[2:2+len(band1)], band1)
	}
	// Band 2 header: yL=0x2D yH=0x00 (45 rows), found after band 1's 255
	// data bytes.
	band2 := []byte{0x1d, 0x76, 0x30, 0x00, 0x01, 0x00, 0x2d, 0x00}
	off := 2 + len(band1) + 255
	if !bytes.Equal(out[off:off+len(band2)], band2) {
		t.Fatalf("band 2 header = % x, want % x", out[off:off+len(band2)], band2)
	}
	// Exactly two raster commands for the whole image.
	if n := bytes.Count(out, []byte{0x1d, 0x76, 0x30, 0x00}); n != 2 {
		t.Fatalf("raster command count = %d, want 2", n)
	}
}

func TestEncodeScalesDownWideImage(t *testing.T) {
	// 16 wide with an 8-dot head → scale to 8 wide; height 4 → 2, aspect
	// preserved. Exact bytes are nearest-neighbour and not asserted; the
	// header dimensions are what matter.
	pngData := grayPNG(t, 16, 4, checker)

	var buf bytes.Buffer
	if err := (ESCPOS{WidthDots: 8}).Encode(&buf, pngData); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()
	if xL := out[6]; xL != 0x01 { // 8 dots → 1 byte/row
		t.Fatalf("scaled xL = %#x, want 0x01", xL)
	}
	if yL := out[8]; yL != 0x02 { // 4*8/16 = 2 rows
		t.Fatalf("scaled yL (rows) = %#x, want 0x02", yL)
	}
	// header(8) + data(1 byte/row × 2 rows) + feed(3) = 13 bytes after ESC @.
	if want := 2 + 8 + 2 + feedLines; len(out) != want {
		t.Fatalf("scaled job length = %d, want %d", len(out), want)
	}
}

func TestEncodeEmptyPayload(t *testing.T) {
	if err := (ESCPOS{WidthDots: 576}).Encode(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("expected error on empty PNG payload")
	}
}

func TestEncodeBadPNG(t *testing.T) {
	if err := (ESCPOS{WidthDots: 576}).Encode(&bytes.Buffer{}, []byte("not a png")); err == nil {
		t.Fatal("expected error decoding non-PNG bytes")
	}
}

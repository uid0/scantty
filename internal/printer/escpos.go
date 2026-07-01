// Package printer encodes claim-tag PNGs as ESC/POS raster jobs and
// writes them straight to a USB receipt printer — no CUPS, no cupsd, no
// per-host print queue. The OMS backend renders the project-storage
// claim tag as a PNG sized for the Epson TM-T printhead; this package
// thresholds it to 1-bit dots, emits the GS v 0 raster command the
// printer firmware understands, and writes the bytes to the usblp
// character device (for example /dev/usb/lp0). The ESC/POS encoding
// that used to be CUPS's job now lives right here.
package printer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
)

// ESC/POS byte sequences with fixed operands. The GS v 0 raster header
// is built inline in writeBand because its size operands are computed.
const (
	escInit    = "\x1b\x40"         // ESC @      initialize printer
	partialCut = "\x1d\x56\x42\x00" // GS V 66 0  feed to cut position + partial cut
)

const (
	// maxBandRows caps how many dot-rows go in one GS v 0 command. The
	// GS v 0 header can address 65535 rows, but the printer's input
	// buffer is small, so we split tall images into horizontal bands.
	// 255 rows keeps each band's payload modest (~18 KB at 576 dots).
	maxBandRows = 255

	// feedLines is how many line feeds advance the paper after the label
	// so the printed area clears the mechanism (and the cutter blade)
	// before we cut.
	feedLines = 3

	// monoThreshold splits the 8-bit luminance range: pixels darker than
	// this become a black dot (bit set); lighter pixels stay blank.
	monoThreshold = 128
)

// ESCPOS renders PNGs to an Epson TM-family (or compatible) receipt
// printer over its raw USB character device.
type ESCPOS struct {
	// Device is the usblp character device, e.g. "/dev/usb/lp0".
	Device string
	// WidthDots is the printhead width in dots: 576 for 80 mm paper on a
	// TM-T20III, 512 for 58 mm. Images wider than this are scaled down
	// preserving aspect ratio; narrower images print at native size.
	WidthDots int
	// Cut requests a partial cut after each label. Printers without an
	// auto-cutter should leave this false.
	Cut bool
}

// Print encodes png and writes the ESC/POS job to the configured USB
// device. The device is opened write-only; usblp forwards the bytes to
// the printer verbatim. Returns an error wrapping the OS failure when
// the device is missing or not writable (see the lp-group / udev notes
// in the systemd unit).
func (e ESCPOS) Print(png []byte) error {
	f, err := os.OpenFile(e.Device, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("printer: open %s: %w", e.Device, err)
	}
	// bufio coalesces the many small writes (init + band headers + data
	// + cut) into fewer syscalls against the character device.
	w := bufio.NewWriter(f)
	if err := e.Encode(w, png); err != nil {
		f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return fmt.Errorf("printer: flush %s: %w", e.Device, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("printer: close %s: %w", e.Device, err)
	}
	return nil
}

// Encode decodes a PNG, thresholds it to a 1-bit raster, and writes a
// complete ESC/POS job — initialize, one or more GS v 0 raster bands,
// then paper feed and (when Cut) a partial cut — to w. It touches no
// hardware, so it is the unit-testable heart of the package.
func (e ESCPOS) Encode(w io.Writer, pngData []byte) error {
	if len(pngData) == 0 {
		return errors.New("printer: empty PNG payload")
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return fmt.Errorf("printer: decode png: %w", err)
	}
	m := e.rasterize(img)

	if _, err := io.WriteString(w, escInit); err != nil {
		return fmt.Errorf("printer: write init: %w", err)
	}
	// Emit the bitmap in horizontal bands of at most maxBandRows so no
	// single GS v 0 payload overruns the printer's input buffer.
	for top := 0; top < m.rows; top += maxBandRows {
		rows := m.rows - top
		if rows > maxBandRows {
			rows = maxBandRows
		}
		band := m.pix[top*m.widthBytes : (top+rows)*m.widthBytes]
		if err := writeBand(w, band, m.widthBytes, rows); err != nil {
			return err
		}
	}
	// Advance the label clear of the mechanism, then cut if configured.
	if _, err := w.Write(bytes.Repeat([]byte{'\n'}, feedLines)); err != nil {
		return fmt.Errorf("printer: write feed: %w", err)
	}
	if e.Cut {
		if _, err := io.WriteString(w, partialCut); err != nil {
			return fmt.Errorf("printer: write cut: %w", err)
		}
	}
	return nil
}

// mono is a 1-bit raster: rows rows of widthBytes bytes each, MSB-first,
// a set bit meaning a black dot. The dot width is widthBytes*8 — the
// source width padded up to a whole byte, with the pad bits left blank.
type mono struct {
	pix        []byte
	widthBytes int
	rows       int
}

// rasterize thresholds img to 1-bit dots, scaling it down (nearest
// neighbour, aspect preserved) when it is wider than WidthDots. OMS
// already renders each label at the printhead width, so the scale path
// is a safety net for a mis-sized PNG rather than the common case.
func (e ESCPOS) rasterize(img image.Image) mono {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()

	dstW, dstH := srcW, srcH
	if e.WidthDots > 0 && srcW > e.WidthDots {
		dstW = e.WidthDots
		dstH = srcH * e.WidthDots / srcW
		if dstH < 1 {
			dstH = 1
		}
	}

	widthBytes := (dstW + 7) / 8 // pad the row width up to a whole byte
	m := mono{
		pix:        make([]byte, widthBytes*dstH),
		widthBytes: widthBytes,
		rows:       dstH,
	}
	for y := 0; y < dstH; y++ {
		sy := b.Min.Y + y*srcH/dstH
		row := m.pix[y*widthBytes : (y+1)*widthBytes]
		for x := 0; x < dstW; x++ {
			sx := b.Min.X + x*srcW/dstW
			if isBlack(img.At(sx, sy)) {
				row[x/8] |= 0x80 >> uint(x%8) // MSB-first, bit set = black
			}
			// Pad bits past dstW stay 0 (blank).
		}
	}
	return m
}

// isBlack reports whether c is dark enough to print as a dot. It uses
// Go's Rec.601 luminance (via color.GrayModel) against monoThreshold.
// Fully transparent pixels convert to black under premultiplied alpha,
// which is fine for the opaque label PNGs OMS renders.
func isBlack(c color.Color) bool {
	return color.GrayModel.Convert(c).(color.Gray).Y < monoThreshold
}

// writeBand emits one GS v 0 raster command: the command + mode prefix,
// the little-endian byte-width (xL, xH) and row count (yL, yH), then the
// band's bitmap bytes. pix must be exactly widthBytes*rows long.
func writeBand(w io.Writer, pix []byte, widthBytes, rows int) error {
	header := []byte{
		0x1d, 0x76, 0x30, 0x00, // GS v 0, m=0
		byte(widthBytes), byte(widthBytes >> 8), // xL, xH  bytes per row
		byte(rows), byte(rows >> 8), // yL, yH  dot rows
	}
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("printer: write raster header: %w", err)
	}
	if _, err := w.Write(pix); err != nil {
		return fmt.Errorf("printer: write raster data: %w", err)
	}
	return nil
}

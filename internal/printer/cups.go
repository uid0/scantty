// Package printer wraps the CUPS `lp` command so callers can stay driver-
// agnostic: anything Linux knows how to print, this prints. The
// project-storage claim tag is a PNG rendered by the OMS backend; CUPS
// rasterizes for the Epson TM-T receipt printer on its own. No ESC/POS
// encoding lives in this package.
package printer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// CUPS prints raw PNG bytes by writing them to a temp file and invoking
// `lp`. Queue is optional — when empty, CUPS uses the system default.
type CUPS struct {
	Queue string // CUPS queue name, e.g. "TM_T20III". Empty = system default.
}

// Print rasterizes a PNG via lp. Returns an error wrapping lp's stderr
// when the printer rejects the job (paper out, cover open, etc.).
func (c CUPS) Print(png []byte) error {
	if len(png) == 0 {
		return errors.New("printer: empty PNG payload")
	}

	f, err := os.CreateTemp("", "claim-*.png")
	if err != nil {
		return fmt.Errorf("printer: tempfile: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(png); err != nil {
		f.Close()
		return fmt.Errorf("printer: write png: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("printer: close png: %w", err)
	}

	args := []string{}
	if c.Queue != "" {
		args = append(args, "-d", c.Queue)
	}
	args = append(args, f.Name())

	cmd := exec.Command("lp", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("printer: lp failed: %w (%s)", err, string(out))
	}
	return nil
}

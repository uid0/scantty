package tui

import (
	"encoding/base64"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// osc52Copy returns the OSC 52 terminal escape that sets the terminal's
// clipboard ("c" selection) to text. Emitting this to the terminal makes the
// *terminal* copy text into the user's system clipboard — which, unlike a
// host-side clipboard call, works even over SSH, so an operator on a remote
// box can paste a scantty-built order pad into a vendor site on their own
// machine. The payload is base64-encoded per the OSC 52 spec and the sequence
// is terminated with BEL (\a). Returns "" for empty input so callers can
// cheaply skip a no-op copy.
func osc52Copy(text string) string {
	if text == "" {
		return ""
	}
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	return "\x1b]52;c;" + enc + "\x07"
}

// copyToClipboardCmd emits an OSC 52 clipboard-set sequence for text to the
// terminal. It's best-effort UX, not load-bearing — the on-screen surface is
// what the operator can always fall back to — so an empty payload is a no-op
// (nil cmd) and any write error is swallowed.
func copyToClipboardCmd(text string) tea.Cmd {
	return clipboardCmdTo(text, os.Stdout)
}

// clipboardCmdTo is the testable core of copyToClipboardCmd: it writes the OSC
// 52 sequence to w. Splitting the writer out lets tests assert the exact bytes
// without touching the real terminal.
//
// Writing to os.Stdout (the same fd Bubbletea renders to) is safe here even
// mid-frame: OSC 52 neither moves the cursor nor emits visible cells, so it
// can't corrupt the alt-screen layout or the renderer's cursor accounting —
// the terminal parses the clipboard escape and the surrounding frame bytes
// independently.
func clipboardCmdTo(text string, w io.Writer) tea.Cmd {
	seq := osc52Copy(text)
	if seq == "" {
		return nil
	}
	return func() tea.Msg {
		_, _ = io.WriteString(w, seq)
		return nil
	}
}

package tui

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestOSC52Copy_Encoding(t *testing.T) {
	// A representative order-pad copy block: tab-separated part#/qty lines.
	text := "ABC-123\t5\nDEF-456\t2"
	got := osc52Copy(text)

	const prefix = "\x1b]52;c;"
	const suffix = "\x07"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("sequence must start with ESC ]52;c; got %q", got)
	}
	if !strings.HasSuffix(got, suffix) {
		t.Fatalf("sequence must end with BEL, got %q", got)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix)
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if string(decoded) != text {
		t.Errorf("decoded payload = %q, want %q", decoded, text)
	}
}

func TestOSC52Copy_EmptyIsNoop(t *testing.T) {
	if got := osc52Copy(""); got != "" {
		t.Errorf("empty text should yield an empty sequence, got %q", got)
	}
}

func TestClipboardCmdTo_WritesSequence(t *testing.T) {
	var buf strings.Builder
	cmd := clipboardCmdTo("XYZ\t1", &buf)
	if cmd == nil {
		t.Fatal("expected a non-nil cmd for non-empty text")
	}
	// The cmd is what actually performs the write; run it as Bubbletea would.
	if msg := cmd(); msg != nil {
		t.Errorf("clipboard cmd should emit no message, got %v", msg)
	}
	if buf.String() != osc52Copy("XYZ\t1") {
		t.Errorf("written bytes = %q, want the OSC 52 sequence", buf.String())
	}
}

func TestClipboardCmdTo_EmptyTextNilCmd(t *testing.T) {
	if cmd := clipboardCmdTo("", &strings.Builder{}); cmd != nil {
		t.Error("empty text should yield a nil cmd (no-op copy)")
	}
}

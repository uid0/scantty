package scanner

import (
	"bufio"
	"context"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type ScanMsg struct {
	Code      string
	Kind      Kind
	Timestamp time.Time
}

type Kind int

const (
	KindUnknown Kind = iota
	KindOMSCode
	KindForgeKeyBadge
)

func Listen(ctx context.Context, src io.Reader, idleFlush time.Duration) tea.Cmd {
	return func() tea.Msg {
		return startedMsg{ctx: ctx, src: src, idleFlush: idleFlush}
	}
}

type startedMsg struct {
	ctx       context.Context
	src       io.Reader
	idleFlush time.Duration
}

func StdinReader() io.Reader { return os.Stdin }

func Classify(code string) Kind {
	switch {
	case len(code) == 0:
		return KindUnknown
	case isForgeKeyBadge(code):
		return KindForgeKeyBadge
	case isOMSCode(code):
		return KindOMSCode
	default:
		return KindUnknown
	}
}

func isOMSCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		default:
			return false
		}
	}
	return true
}

func isForgeKeyBadge(code string) bool {
	if len(code) < 8 || len(code) > 20 {
		return false
	}
	for _, r := range code {
		if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func ReadOne(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line, nil
}

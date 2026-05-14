package scanner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
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
	KindOMSURL
)

type URLTarget struct {
	Kind       string
	ResourceID string
	RawPath    string
}

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
	case isURL(code):
		return KindOMSURL
	case isForgeKeyBadge(code):
		return KindForgeKeyBadge
	case isOMSCode(code):
		return KindOMSCode
	default:
		return KindUnknown
	}
}

func isURL(code string) bool {
	return strings.HasPrefix(code, "http://") || strings.HasPrefix(code, "https://")
}

func ParseOMSURL(raw string) (*URLTarget, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("scanner: parse url: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return nil, fmt.Errorf("scanner: empty url path")
	}
	t := &URLTarget{RawPath: u.Path}
	switch {
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "items":
		t.Kind = "item"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "scan":
		// /inventory/scan/<itemId> or /inventory/scan/asset/<id> etc.
		if len(parts) == 3 {
			t.Kind = "code"
			t.ResourceID = parts[2]
		} else {
			t.Kind = parts[2]
			t.ResourceID = parts[3]
		}
	case len(parts) >= 2 && parts[0] == "assets":
		t.Kind = "asset"
		t.ResourceID = parts[1]
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "locations":
		t.Kind = "location"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "inventory" && parts[1] == "suppliers":
		t.Kind = "supplier"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "purchasing" && parts[1] == "orders":
		t.Kind = "purchase_order"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "maintenance" && parts[1] == "work-orders":
		t.Kind = "work_order"
		t.ResourceID = parts[2]
	case len(parts) >= 3 && parts[0] == "maintenance" && parts[1] == "third-party":
		t.Kind = "third_party_work_order"
		t.ResourceID = parts[2]
	case len(parts) >= 2 && parts[0] == "sigs":
		t.Kind = "sig"
		t.ResourceID = parts[1]
	case len(parts) >= 3 && parts[0] == "facilities" && parts[1] == "forgekey-devices":
		t.Kind = "forgekey_device"
		t.ResourceID = parts[2]
	default:
		t.Kind = "unknown"
		t.ResourceID = strings.Join(parts, "/")
	}
	return t, nil
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

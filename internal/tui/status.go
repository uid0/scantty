package tui

import (
	"fmt"
	"strings"
	"time"
)

type StatusBar struct {
	width     int
	connOMS   bool
	connFK    bool
	scanner   string
	message   string
	msgLevel  StatusLevel
	msgExpiry time.Time
}

func NewStatusBar() StatusBar {
	return StatusBar{scanner: "idle"}
}

func (s *StatusBar) SetWidth(w int)            { s.width = w }
func (s *StatusBar) SetOMSConn(ok bool)        { s.connOMS = ok }
func (s *StatusBar) SetForgeKeyConn(ok bool)   { s.connFK = ok }
func (s *StatusBar) SetScanner(state string)   { s.scanner = state }

func (s *StatusBar) Flash(text string, level StatusLevel, ttl time.Duration) {
	s.message = text
	s.msgLevel = level
	if ttl <= 0 {
		ttl = 4 * time.Second
	}
	s.msgExpiry = time.Now().Add(ttl)
}

func conn(name string, ok bool) string {
	if ok {
		return StyleStatusOK.Render(fmt.Sprintf("● %s", name))
	}
	return StyleStatusError.Render(fmt.Sprintf("○ %s", name))
}

func (s StatusBar) View() string {
	left := strings.Join([]string{
		conn("OMS", s.connOMS),
		conn("FK", s.connFK),
		StyleStatusInfo.Render(fmt.Sprintf("scanner: %s", s.scanner)),
	}, "  ")

	right := ""
	if s.message != "" && time.Now().Before(s.msgExpiry) {
		right = RenderStatus(s.message, s.msgLevel)
	} else {
		right = StyleMuted.Render("q quit · tab nav · / search")
	}

	gap := s.width - lenVis(left) - lenVis(right)
	if gap < 1 {
		gap = 1
	}
	body := left + strings.Repeat(" ", gap) + right
	return StyleStatusBar.Width(s.width).Render(body)
}

func lenVis(s string) int {
	n := 0
	skip := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			skip = true
		case skip && r == 'm':
			skip = false
		case !skip:
			n++
		}
	}
	return n
}

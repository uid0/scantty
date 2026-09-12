package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type NotificationsScreen struct {
	deps           Deps
	rows           []omsapi.Notification
	loading        bool
	loadErr        string
	scroller       *TextScroller
	terminalHeight int
	terminalWidth  int
}

type notificationsLoadedMsg struct {
	rows []omsapi.Notification
	err  error
}

type notificationActionMsg struct {
	label string
	err   error
}

type NotificationPollMsg struct {
	rows []omsapi.Notification
	err  error
}

func NewNotificationsScreen(deps Deps) *NotificationsScreen {
	return &NotificationsScreen{deps: deps, loading: true, scroller: NewTextScroller(defaultDetailHeight)}
}

func (s *NotificationsScreen) Title() string { return "Notifications" }

func (s *NotificationsScreen) Init() tea.Cmd { return s.load() }

func (s *NotificationsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListNotifications(ctx, nil)
		if err != nil {
			return notificationsLoadedMsg{err: err}
		}
		return notificationsLoadedMsg{rows: page.Results}
	}
}

func (s *NotificationsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.terminalWidth = m.Width
		proseSizeScroller(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
		return s, nil
	case notificationsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.rows
		s.scroller.Set(s.renderBody())
		// New rows arrived — pin to the top so the operator sees the
		// most-recent notifications (rows are already sorted
		// newest-first by the backend) instead of staying scrolled
		// into the middle of the previous page.
		s.scroller.Top()
		return s, nil
	case notificationActionMsg:
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.label, m.err.Error()), StatusError)
		}
		return s, tea.Batch(Status(m.label, StatusOK), s.load())
	case tea.KeyMsg:
		// Let the scroller eat j/k/pgup/pgdn/g/G so the operator can
		// pan the full notification list. Without this the older
		// notifications sat above the viewport with no way to bring
		// them back into view (the bug uid0 hit on 2026-06-13).
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "X":
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			return s, func() tea.Msg {
				err := deps.OMS.MarkAllNotificationsRead(ctx)
				return notificationActionMsg{label: "marked all read", err: err}
			}
		}
	}
	return s, nil
}

func (s *NotificationsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading notifications…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		// THE EMPTY STATE DRAWS A BAR, which it did not before: it returned the
		// fact alone, so the one state where "there is nothing here, now what?"
		// is the operator's actual question was the one state naming no key at
		// all — the bar's contract inverted, exactly as ListScreen's empty
		// branch had it (AGENTS.md). The bar is short there because it is
		// honest: nothing scrolls, so the movement segments are absent, and `X`
		// is not named because marking nothing read is a round trip whose whole
		// product is invisible.
		return StyleMuted.Render("No notifications.") + "\n\n" +
			s.bar(false).render(proseBarCells(s.terminalWidth))
	}
	return proseScrollFrame(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
}

// bar names every key that acts on this sheet, as a record the honesty sweep
// can press (prose_bar.go).
//
// The literal this replaced read "j/k scroll · pgup/pgdn page · g/G top/bottom ·
// X mark all read · r refresh · esc back", and its omission was the hardest of
// the set to see: it named six of the vocabulary's ten keystrokes and left out
// exactly the two an operator whose hands are already on the arrow cluster
// reaches for — the ARROWS and home/end — while running to 85 cells against the
// 51 an 80-column pane gives, so clampToBox was taking `r refresh · esc back`
// off the end. Named-past-the-cut and never-named at once.
//
// `X` IS GATED ON THERE BEING ROWS, and on that rather than on the movement
// answer: a short list that does not scroll still has notifications to mark, so
// `scrolls` is the wrong question. With NO rows the key is a POST whose whole
// visible product is nothing at all, which is a key named on a frame it cannot
// be seen to act on — the same threshold ListScreen keeps `enter open` behind.
func (s *NotificationsScreen) bar(scrolls bool) proseBar {
	out := proseNavScroll(scrolls)
	if len(s.rows) > 0 {
		out = append(out, proseBarItem{Keys: []string{"X"}, Hint: "X mark all read"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this sheet is DRAWING — nil in the two load states, which
// draw their own line instead. The EMPTY state is NOT one of them: it draws a
// bar now (see View), so the sweep presses keys at it like any other.
func (s *NotificationsScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return nil
	}
	if len(s.rows) == 0 {
		return s.bar(false)
	}
	return proseScrollBar(s.scroller, s.terminalHeight, proseBarCells(s.terminalWidth), s.bar)
}

// renderBody draws every row into a single string for the scroller.
// Newest rows come first because the backend orders by -created_at;
// the scroller starts at the top of the rendered string so the
// operator sees today's notifications immediately, then scrolls down
// for the history.
func (s *NotificationsScreen) renderBody() string {
	var b strings.Builder
	for _, n := range s.rows {
		dot := StyleStatusWarn.Render("●")
		if n.Read {
			dot = StyleMuted.Render("○")
		}
		ts := ""
		if !n.CreatedAt.IsZero() {
			ts = " " + StyleMuted.Render(n.CreatedAt.Format("01-02 15:04"))
		}
		title := "  " + dot + " " + n.Title + StyleMuted.Render(" ["+n.Type+"]") + ts
		b.WriteString(title + "\n")
		if n.Message != "" {
			b.WriteString("    " + StyleMuted.Render(n.Message) + "\n")
		}
	}
	return b.String()
}

func PollNotifications(deps Deps, interval time.Duration) tea.Cmd {
	if deps.OMS == nil || deps.OMS.AccessToken() == "" {
		return nil
	}
	return tea.Tick(interval, func(time.Time) tea.Msg {
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		page, err := deps.OMS.ListUnreadNotifications(ctx)
		if err != nil || page == nil {
			return NotificationPollMsg{err: err}
		}
		return NotificationPollMsg{rows: page.Results}
	})
}

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
		return StyleMuted.Render("No notifications.")
	}
	// Footer rows: blank-line + hint. detailFooterRows is the count
	// the scroller subtracts from terminal height to compute the
	// content viewport.
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	hint := "j/k scroll · pgup/pgdn page · g/G top/bottom · X mark all read · r refresh · esc back"
	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
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

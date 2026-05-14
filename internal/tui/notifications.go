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
	deps    Deps
	rows    []omsapi.Notification
	cursor  int
	loading bool
	loadErr string
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
	return &NotificationsScreen{deps: deps, loading: true}
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
	case notificationsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case notificationActionMsg:
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.label, m.err.Error()), StatusError)
		}
		return s, tea.Batch(Status(m.label, StatusOK), s.load())
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "x", "enter":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			if row.Read {
				return s, Status("already read", StatusWarn)
			}
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			id := row.ID
			return s, func() tea.Msg {
				err := deps.OMS.MarkNotificationRead(ctx, id)
				return notificationActionMsg{label: fmt.Sprintf("marked %v read", id), err: err}
			}
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
	var b strings.Builder
	for i, n := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		dot := StyleStatusWarn.Render("●")
		if n.Read {
			dot = StyleMuted.Render("○")
		}
		ts := ""
		if !n.CreatedAt.IsZero() {
			ts = " " + StyleMuted.Render(n.CreatedAt.Format("01-02 15:04"))
		}
		title := caret + dot + " " + n.Title + StyleMuted.Render(" ["+n.Type+"]") + ts
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		b.WriteString(title + "\n")
		if n.Message != "" {
			b.WriteString("    " + StyleMuted.Render(n.Message) + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · x/enter mark read · X mark all · r refresh · esc back"))
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

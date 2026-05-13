package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent   = lipgloss.Color("213")
	colorMuted    = lipgloss.Color("245")
	colorBorder   = lipgloss.Color("240")
	colorOK       = lipgloss.Color("42")
	colorWarn     = lipgloss.Color("214")
	colorError    = lipgloss.Color("203")
	colorSelected = lipgloss.Color("236")

	StyleTitle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	StyleMuted = lipgloss.NewStyle().Foreground(colorMuted)

	StyleSidebar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(colorBorder).
			Padding(0, 1)

	StyleSidebarItem         = lipgloss.NewStyle().Padding(0, 1)
	StyleSidebarItemActive   = StyleSidebarItem.Foreground(colorAccent).Bold(true).Background(colorSelected)
	StyleSidebarItemDisabled = StyleSidebarItem.Foreground(colorMuted)

	StyleContent = lipgloss.NewStyle().Padding(1, 2)

	StyleStatusBar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(colorBorder).
			Padding(0, 1)

	StyleStatusInfo  = lipgloss.NewStyle().Foreground(colorMuted)
	StyleStatusOK    = lipgloss.NewStyle().Foreground(colorOK)
	StyleStatusWarn  = lipgloss.NewStyle().Foreground(colorWarn)
	StyleStatusError = lipgloss.NewStyle().Foreground(colorError)
)

func RenderStatus(text string, level StatusLevel) string {
	switch level {
	case StatusOK:
		return StyleStatusOK.Render(text)
	case StatusWarn:
		return StyleStatusWarn.Render(text)
	case StatusError:
		return StyleStatusError.Render(text)
	default:
		return StyleStatusInfo.Render(text)
	}
}

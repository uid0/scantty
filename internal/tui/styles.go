package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/theme"
)

var (
	currentTheme  = theme.DefaultName
	colorAccent   = lipgloss.Color(theme.Default().Accent)
	colorMuted    = lipgloss.Color("245")
	colorBorder   = lipgloss.Color("240")
	colorOK       = lipgloss.Color("42")
	colorWarn     = lipgloss.Color("214")
	colorError    = lipgloss.Color("203")
	colorSelected = lipgloss.Color("236")

	StyleTitle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	StyleMuted = lipgloss.NewStyle().Foreground(colorMuted)

	// StyleMetricLabel bolds a metrics-row label (QOH:/QOO:/…/Cost:) so the
	// Q's & Costs line draws the eye (Ian UX). Bold adds no display width, so
	// the fixed-width, right-aligned value columns still line up — a wider glyph
	// would have shifted them.
	StyleMetricLabel = lipgloss.NewStyle().Bold(true)

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

func ApplyTheme(name string) string {
	def, ok := theme.Lookup(name)
	if !ok {
		def = theme.Default()
	}
	currentTheme = def.Name
	colorAccent = lipgloss.Color(def.Accent)
	StyleTitle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	StyleSidebarItemActive = StyleSidebarItem.Foreground(colorAccent).Bold(true).Background(colorSelected)
	return currentTheme
}

func CurrentTheme() string {
	return currentTheme
}

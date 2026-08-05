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

	// StyleSidebarCursor marks the sidebar row the keyboard is on, and only
	// while the sidebar HOLDS the keyboard. It is deliberately the same
	// reverse-video treatment a focused field gets on a columnar sheet
	// (StyleJDEFieldFocused) rather than the active-workspace highlight, so the
	// two readings stay distinct: bold-on-selected says "the screen you are
	// looking at", reverse says "where the next keypress goes".
	StyleSidebarCursor = StyleSidebarItem.Reverse(true).Foreground(colorAccent)

	StyleContent = lipgloss.NewStyle().Padding(1, 2)

	StyleStatusBar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(colorBorder).
			Padding(0, 1)

	StyleStatusInfo  = lipgloss.NewStyle().Foreground(colorMuted)
	StyleStatusOK    = lipgloss.NewStyle().Foreground(colorOK)
	StyleStatusWarn  = lipgloss.NewStyle().Foreground(colorWarn)
	StyleStatusError = lipgloss.NewStyle().Foreground(colorError)

	// Columnar green-screen form styles (jde_form.go). The label column, the
	// dotted leader and the underscored input area are all quiet; the FOCUSED
	// row is the only loud thing on the sheet, which is what a form navigated
	// by arrow keys alone needs. Everything that carries the highlight follows
	// the accent, so a theme change moves it — see ApplyTheme.
	StyleJDELabel        = lipgloss.NewStyle()
	StyleJDELabelFocused = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	StyleJDELeader       = lipgloss.NewStyle().Foreground(colorBorder)
	StyleJDEInput        = lipgloss.NewStyle().Foreground(colorBorder)
	StyleJDEFieldFocused = lipgloss.NewStyle().Reverse(true).Foreground(colorAccent)
	StyleJDEBracket      = lipgloss.NewStyle().Foreground(colorMuted)
	StyleJDEHint         = lipgloss.NewStyle().Foreground(colorMuted)
	StyleJDEHeading      = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)

	// The persistent action bar: a quiet rule, the keys in the accent, what
	// they do beside them.
	StyleActionBar     = lipgloss.NewStyle().Foreground(colorMuted)
	StyleActionBarKey  = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	StyleActionBarRule = lipgloss.NewStyle().Foreground(colorBorder)
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
	StyleSidebarCursor = StyleSidebarItem.Reverse(true).Foreground(colorAccent)
	// The columnar-form highlight and the action-bar keys are accent-coloured,
	// so they have to be rebuilt here too — a style built once at package init
	// would keep the theme the app started with.
	StyleJDELabelFocused = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	StyleJDEFieldFocused = lipgloss.NewStyle().Reverse(true).Foreground(colorAccent)
	StyleJDEHeading = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	StyleActionBarKey = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	return currentTheme
}

func CurrentTheme() string {
	return currentTheme
}

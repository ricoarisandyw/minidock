package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Adaptive colors: the first value is used on light terminals, the second on dark ones.
var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#5B3FD6", Dark: "#A594FF"}
	colorOK     = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	colorErr    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#FF7B72"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	colorText   = lipgloss.AdaptiveColor{Light: "#1F2328", Dark: "#E6EDF3"}
)

var (
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	styleOK      = lipgloss.NewStyle().Foreground(colorOK)
	styleErr     = lipgloss.NewStyle().Foreground(colorErr)
	styleWarn    = lipgloss.NewStyle().Foreground(colorWarn)
	styleBold    = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	styleKey     = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleBadge   = lipgloss.NewStyle().Foreground(colorWarn)
	styleCode    = lipgloss.NewStyle().Foreground(colorText)
	styleTabOn   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 1).Underline(true)
	styleTabOff  = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)
	styleRowOn   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleRowOff  = lipgloss.NewStyle().Foreground(colorText)
	stylePanel   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorMuted).Padding(0, 1)
	styleBanner  = lipgloss.NewStyle().Foreground(colorErr).Bold(true)
	styleDivider = lipgloss.NewStyle().Foreground(colorMuted)
)

// trunc cuts s to at most w display cells, adding an ellipsis when it was cut.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// pad cuts or pads s to exactly w display cells.
func pad(s string, w int) string {
	s = trunc(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += lipgloss.NewStyle().Width(gap).Render("")
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// kv renders a "label  value" line with an aligned muted label.
func kv(label, value string) string {
	return styleMuted.Render(pad(label, 11)) + value
}

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"minidock/internal/docker"
)

// renderStatusBar draws the Docker status on the left and a message on the right,
// followed by the key hints.
func renderStatusBar(width int, st docker.Status, known bool, toast string, toastErr bool, hints []hint) string {
	var left string
	switch {
	case !known:
		left = styleMuted.Render("● Checking Docker…")
	case st.Connected:
		v := ""
		if st.Version != "" {
			v = " " + st.Version
		}
		left = styleOK.Render("● Docker connected" + v)
	default:
		left = styleErr.Render("● Docker not running")
	}

	right := ""
	if toast != "" {
		style := styleOK
		if toastErr {
			style = styleErr
		}
		right = style.Render(trunc(toast, maxInt(10, width-lipgloss.Width(left)-4)))
	}
	gap := maxInt(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	line1 := left + strings.Repeat(" ", gap) + right

	line2 := trunc(renderHints(hints), width)
	return line1 + "\n" + line2
}

// renderBanner is the red box shown above the content when Docker is down.
func renderBanner(width int, st docker.Status) string {
	msg := st.Hint
	if msg == "" {
		msg = "Docker is not running."
	}
	return styleBanner.Render(trunc("⚠  "+msg, width))
}

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"minidock/internal/docker"
)

// serversView lists the containers MiniDock created.
type serversView struct {
	items  []docker.Instance
	cursor int
	loaded bool
	err    error
}

// SetItems replaces the list, keeping the cursor on the same server.
func (s *serversView) SetItems(items []docker.Instance, err error) {
	var keep string
	if cur, ok := s.selected(); ok {
		keep = cur.ID
	}
	s.items, s.err, s.loaded = items, err, true
	if err != nil {
		return
	}
	for i, it := range items {
		if it.ID == keep {
			s.cursor = i
		}
	}
	s.clamp()
}

func (s *serversView) clamp() {
	if s.cursor >= len(s.items) {
		s.cursor = len(s.items) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *serversView) selected() (docker.Instance, bool) {
	if s.cursor < 0 || s.cursor >= len(s.items) {
		return docker.Instance{}, false
	}
	return s.items[s.cursor], true
}

// byID finds a server by container id.
func (s *serversView) byID(id string) (docker.Instance, bool) {
	for _, it := range s.items {
		if it.ID == id {
			return it, true
		}
	}
	return docker.Instance{}, false
}

// HandleKey handles navigation and shortcuts. Actions are sent back as serverActionMsg.
// The bool is true when the key was used; open is true when enter asks for details.
func (s *serversView) HandleKey(msg tea.KeyMsg) (handled bool, open bool, cmd tea.Cmd) {
	inst, has := s.selected()
	act := func(kind string) tea.Cmd {
		if !has {
			return nil
		}
		return func() tea.Msg { return serverActionMsg{kind: kind, inst: inst} }
	}
	switch msg.String() {
	case "up", "k":
		s.cursor--
		s.clamp()
	case "down", "j":
		s.cursor++
		s.clamp()
	case "home", "g":
		s.cursor = 0
	case "end", "G":
		s.cursor = len(s.items) - 1
		s.clamp()
	case "enter":
		return true, has, nil
	case "s":
		return true, false, act("toggle")
	case "R":
		return true, false, act("restart")
	case "c":
		return true, false, act("copy")
	case "m":
		return true, false, act("mcp")
	case "d":
		return true, false, act("delete")
	default:
		return false, false, nil
	}
	return true, false, nil
}

func stateDot(inst docker.Instance) string {
	switch {
	case inst.Running():
		return styleOK.Render("●")
	case inst.State == "paused":
		return styleWarn.Render("●")
	default:
		return styleMuted.Render("○")
	}
}

// View draws the table of servers.
func (s *serversView) View(w, h int) string {
	switch {
	case !s.loaded:
		return styleMuted.Render("Loading servers…")
	case s.err != nil:
		return styleErr.Render(lipgloss.NewStyle().Width(maxInt(10, w)).Render("Could not load servers: " + docker.Explain(s.err)))
	case len(s.items) == 0:
		return styleMuted.Render(lipgloss.NewStyle().Width(maxInt(10, w)).Render(
			"No servers yet.\nOpen the Templates tab and press enter on a template to create your first one."))
	}

	const portW, statusW = 7, 20
	tplW := 16
	if w < 70 {
		tplW = 0
	}
	nameW := maxInt(8, w-2-portW-statusW-tplW-3)

	h = maxInt(h-1, 1)
	start := 0
	if s.cursor >= h {
		start = s.cursor - h + 1
	}
	end := minInt(len(s.items), start+h)

	header := styleMuted.Render(pad("  "+pad("NAME", nameW)+" "+
		ifThen(tplW > 0, pad("TEMPLATE", tplW)+" ", "")+pad("PORT", portW)+" "+"STATUS", w))
	rows := []string{header}
	for i := start; i < end; i++ {
		it := s.items[i]
		tpl := it.TemplateID
		if it.Version != "" {
			tpl += " " + it.Version
		}
		line := pad(it.Name, nameW) + " " +
			ifThen(tplW > 0, pad(tpl, tplW)+" ", "") +
			pad(fmt.Sprintf(":%d", it.HostPort), portW) + " " +
			pad(it.StatusText, statusW)
		if i == s.cursor {
			rows = append(rows, stateDot(it)+" "+styleRowOn.Render(pad(line, w-2)))
		} else {
			rows = append(rows, stateDot(it)+" "+styleRowOff.Render(pad(line, w-2)))
		}
	}
	return strings.Join(rows, "\n")
}

func ifThen(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

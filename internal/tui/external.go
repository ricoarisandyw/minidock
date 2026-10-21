package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"minidock/internal/docker"
	"minidock/internal/external"
)

// externalView lists the saved connections to databases MiniDock does not run.
type externalView struct {
	items  []external.Conn
	cursor int
	// live is the container behind each connection, by connection name. A
	// connection whose container is missing right now simply has no entry.
	live map[string]docker.Foreign
}

// SetLive records the containers MiniDock found, so the list can show which
// connections point at something that is actually running.
func (v *externalView) SetLive(found []docker.Foreign) {
	byName := make(map[string]docker.Foreign, len(found))
	for _, f := range found {
		byName[f.Name] = f
	}
	live := make(map[string]docker.Foreign, len(v.items))
	for _, c := range v.items {
		if f, ok := byName[c.Container]; ok {
			live[c.Name] = f
		}
	}
	v.live = live
}

// container returns the live container behind a connection, if it is there.
func (v *externalView) container(c external.Conn) (docker.Foreign, bool) {
	f, ok := v.live[c.Name]
	return f, ok
}

// SetItems replaces the list, keeping the cursor on the same connection.
func (v *externalView) SetItems(items []external.Conn) {
	var keep string
	if cur, ok := v.selected(); ok {
		keep = cur.Name
	}
	v.items = items
	for i, it := range items {
		if it.Name == keep {
			v.cursor = i
		}
	}
	v.clamp()
}

// selectName puts the cursor on a connection by name.
func (v *externalView) selectName(name string) {
	for i, it := range v.items {
		if it.Name == name {
			v.cursor = i
			return
		}
	}
}

func (v *externalView) clamp() {
	if v.cursor >= len(v.items) {
		v.cursor = len(v.items) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
}

func (v *externalView) selected() (external.Conn, bool) {
	if v.cursor < 0 || v.cursor >= len(v.items) {
		return external.Conn{}, false
	}
	return v.items[v.cursor], true
}

// HandleKey handles navigation and shortcuts, reporting actions as externalActionMsg.
func (v *externalView) HandleKey(msg tea.KeyMsg) (handled bool, cmd tea.Cmd) {
	conn, has := v.selected()
	act := func(kind string) tea.Cmd {
		if !has {
			return nil
		}
		return func() tea.Msg { return externalActionMsg{kind: kind, conn: conn} }
	}
	switch msg.String() {
	case "up", "k":
		v.cursor--
		v.clamp()
	case "down", "j":
		v.cursor++
		v.clamp()
	case "home", "g":
		v.cursor = 0
	case "end", "G":
		v.cursor = len(v.items) - 1
		v.clamp()
	case "n":
		return true, func() tea.Msg { return externalActionMsg{kind: "new"} }
	case "e", "enter":
		return true, act("edit")
	case "d":
		return true, act("delete")
	case "m":
		return true, act("mcp")
	case "t":
		return true, act("test")
	case "s":
		return true, act("toggle")
	case "R":
		return true, act("restart")
	default:
		return false, nil
	}
	return true, nil
}

// View draws the table of saved connections.
func (v *externalView) View(w, h int) string {
	if len(v.items) == 0 {
		return styleMuted.Render(lipgloss.NewStyle().Width(maxInt(10, w)).Render(
			"No connections to existing databases yet.\n\n" +
				"Press n to point MiniDock at a database container it did not create —\n" +
				"for example one from a docker-compose project — so your AI tool can read it.\n\n" +
				"Access stays read-only."))
	}

	const clientW, statusW = 9, 20
	noteW := 20
	if w < 96 {
		noteW = 0
	}
	nameW := maxInt(10, (w-2-clientW-statusW-noteW-4)/2)
	targetW := maxInt(10, w-2-nameW-clientW-statusW-noteW-4)

	h = maxInt(h-1, 1)
	start := 0
	if v.cursor >= h {
		start = v.cursor - h + 1
	}
	end := minInt(len(v.items), start+h)

	header := styleMuted.Render(pad("  "+pad("NAME", nameW)+" "+pad("DATABASE", targetW)+" "+
		pad("CLIENT", clientW)+" "+pad("STATUS", statusW)+ifThen(noteW > 0, " NOTE", ""), w))
	rows := []string{header}
	for i := start; i < end; i++ {
		it := v.items[i]
		status := "not found"
		if f, ok := v.container(it); ok {
			status = f.StatusText
		}
		line := pad(it.Name, nameW) + " " + pad(it.Describe(), targetW) + " " + pad(it.Client, clientW) +
			" " + pad(status, statusW) + ifThen(noteW > 0, " "+pad(it.Note, noteW), "")
		dot := styleMuted.Render("○")
		if f, ok := v.container(it); ok {
			switch {
			case f.Running():
				dot = styleOK.Render("●")
			case f.State == "paused":
				dot = styleWarn.Render("●")
			}
		}
		if i == v.cursor {
			rows = append(rows, dot+" "+styleRowOn.Render(pad(line, w-2)))
		} else {
			rows = append(rows, dot+" "+styleRowOff.Render(pad(line, w-2)))
		}
	}
	return strings.Join(rows, "\n")
}

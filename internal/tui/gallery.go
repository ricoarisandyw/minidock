package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"minidock/internal/template"
)

// gallery is a searchable list of templates with a preview panel.
type gallery struct {
	items     []template.Template
	cursor    int
	filter    textinput.Model
	filtering bool
	empty     string
}

func newGallery(empty string) gallery {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "search templates"
	ti.CharLimit = 40
	return gallery{filter: ti, empty: empty}
}

// SetItems replaces the list and keeps the cursor on the same template if possible.
func (g *gallery) SetItems(items []template.Template) {
	var keep string
	if t, ok := g.selected(); ok {
		keep = t.ID
	}
	g.items = items
	g.cursor = 0
	if keep != "" {
		for i, t := range g.visible() {
			if t.ID == keep {
				g.cursor = i
				break
			}
		}
	}
	g.clamp()
}

func (g *gallery) visible() []template.Template {
	q := strings.ToLower(strings.TrimSpace(g.filter.Value()))
	if q == "" {
		return g.items
	}
	var out []template.Template
	for _, t := range g.items {
		hay := strings.ToLower(t.ID + " " + t.Name + " " + t.Description + " " + t.Image)
		if strings.Contains(hay, q) {
			out = append(out, t)
		}
	}
	return out
}

func (g *gallery) clamp() {
	n := len(g.visible())
	if g.cursor >= n {
		g.cursor = n - 1
	}
	if g.cursor < 0 {
		g.cursor = 0
	}
}

func (g *gallery) selected() (template.Template, bool) {
	vis := g.visible()
	if g.cursor < 0 || g.cursor >= len(vis) {
		return template.Template{}, false
	}
	return vis[g.cursor], true
}

// selectID moves the cursor to the template with this id, if it is visible.
func (g *gallery) selectID(id string) {
	for i, t := range g.visible() {
		if t.ID == id {
			g.cursor = i
			return
		}
	}
}

// Filtering reports whether the search box has the keyboard.
func (g *gallery) Filtering() bool { return g.filtering }

// HandleKey handles list navigation and search. It returns true when it used the key.
func (g *gallery) HandleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if g.filtering {
		switch msg.String() {
		case "enter":
			g.filtering = false
			g.filter.Blur()
			return true, nil
		case "esc":
			g.filtering = false
			g.filter.SetValue("")
			g.filter.Blur()
			g.cursor = 0
			return true, nil
		case "up", "down":
			g.move(map[string]int{"up": -1, "down": 1}[msg.String()])
			return true, nil
		}
		var cmd tea.Cmd
		g.filter, cmd = g.filter.Update(msg)
		g.cursor = 0
		g.clamp()
		return true, cmd
	}

	switch msg.String() {
	case "up", "k":
		g.move(-1)
	case "down", "j":
		g.move(1)
	case "home", "g":
		g.cursor = 0
	case "end", "G":
		g.cursor = len(g.visible()) - 1
		g.clamp()
	case "/":
		g.filtering = true
		return true, g.filter.Focus()
	case "esc":
		if g.filter.Value() != "" {
			g.filter.SetValue("")
			g.cursor = 0
			return true, nil
		}
		return false, nil
	default:
		return false, nil
	}
	return true, nil
}

func (g *gallery) move(delta int) {
	g.cursor += delta
	g.clamp()
}

// View draws the list and, when there is room, the preview next to it.
func (g *gallery) View(w, h int) string {
	top := ""
	if g.filtering || g.filter.Value() != "" {
		top = g.filter.View() + "\n"
		h--
	}
	vis := g.visible()
	if len(vis) == 0 {
		msg := g.empty
		if g.filter.Value() != "" {
			msg = "No template matches “" + g.filter.Value() + "”."
		}
		return top + styleMuted.Render(lipgloss.NewStyle().Width(maxInt(10, w)).Render(msg))
	}

	listW := 30
	if w < 70 {
		listW = w
	}
	list := g.renderList(vis, listW, h)
	if w < 70 {
		return top + list
	}
	sel, _ := g.selected()
	preview := renderPreview(sel, w-listW-1, h)
	return top + lipgloss.JoinHorizontal(lipgloss.Top, list, " ", preview)
}

func (g *gallery) renderList(vis []template.Template, w, h int) string {
	h = maxInt(h, 1)
	start := 0
	if g.cursor >= h {
		start = g.cursor - h + 1
	}
	end := minInt(len(vis), start+h)

	rows := make([]string, 0, h)
	for i := start; i < end; i++ {
		t := vis[i]
		label := t.Title()
		if !t.Builtin {
			label += " " + styleBadge.Render("custom")
		}
		if i == g.cursor {
			rows = append(rows, styleRowOn.Render(pad("▸ "+label, w)))
		} else {
			rows = append(rows, styleRowOff.Render(pad("  "+label, w)))
		}
	}
	return strings.Join(rows, "\n")
}

func renderPreview(t template.Template, w, h int) string {
	inner := maxInt(10, w-4)
	var b strings.Builder
	b.WriteString(styleTitle.Render(t.Title()))
	if !t.Builtin {
		b.WriteString("  " + styleBadge.Render("custom"))
	}
	b.WriteString("\n")
	if t.Description != "" {
		b.WriteString(lipgloss.NewStyle().Width(inner).Foreground(colorMuted).Render(t.Description) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(kv("Image", t.Image) + "\n")
	if len(t.Versions) > 0 {
		b.WriteString(kv("Versions", trunc(strings.Join(t.Versions, ", "), inner-11)) + "\n")
	}
	b.WriteString(kv("Port", fmt.Sprintf("%d  (container %d)", t.HostPort(), t.ContainerPort)) + "\n")
	if t.DataPath != "" {
		b.WriteString(kv("Data", t.DataPath+" (kept in a Docker volume)") + "\n")
	}
	if len(t.Fields) > 0 {
		labels := make([]string, 0, len(t.Fields))
		for _, f := range t.Fields {
			labels = append(labels, f.Label)
		}
		b.WriteString(kv("Asks for", trunc(strings.Join(labels, ", "), inner-11)) + "\n")
	}
	b.WriteString("\n" + styleMuted.Render("Press enter to set up a server"))

	return stylePanel.Width(maxInt(10, w-2)).MaxHeight(h).Render(b.String())
}

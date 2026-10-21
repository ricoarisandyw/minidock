package tui

import (
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func itoa(n int) string { return strconv.Itoa(n) }

// copyToClipboard puts text on the system clipboard.
// It fails on machines without a clipboard tool (for example a bare SSH session).
// It is a variable so tests can swap it and leave the real clipboard alone.
var copyToClipboard = func(text string) error { return clipboard.WriteAll(text) }

// confirmOpt is one answer in a confirm dialog.
type confirmOpt struct{ key, label, value string }

// confirm is a small modal question answered with a single key.
type confirm struct {
	id    string
	title string
	body  string
	opts  []confirmOpt
}

// Update answers on a matching key; esc or n cancels (empty value).
func (c *confirm) Update(msg tea.KeyMsg) tea.Cmd {
	choose := func(v string) tea.Cmd {
		return func() tea.Msg { return confirmChoiceMsg{id: c.id, value: v} }
	}
	k := msg.String()
	for _, o := range c.opts {
		if k == o.key {
			return choose(o.value)
		}
	}
	if k == "esc" || k == "n" {
		return choose("")
	}
	return nil
}

func (c *confirm) View(w int) string {
	var b strings.Builder
	b.WriteString(styleWarn.Render(c.title) + "\n\n")
	if c.body != "" {
		b.WriteString(lipgloss.NewStyle().Width(maxInt(20, minInt(w-6, 70))).Render(c.body) + "\n\n")
	}
	for _, o := range c.opts {
		b.WriteString(styleKey.Render("["+o.key+"]") + " " + o.label + "\n")
	}
	b.WriteString(styleKey.Render("[n]") + " " + styleMuted.Render("Cancel"))
	return stylePanel.Render(b.String())
}

// prompt is a one-line text question.
type prompt struct {
	id    string
	title string
	help  string
	input textinput.Model
}

func newPrompt(id, title, help, initial string) (*prompt, tea.Cmd) {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.Width = 60
	return &prompt{id: id, title: title, help: help, input: ti}, ti.Focus()
}

func (p *prompt) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			v := strings.TrimSpace(p.input.Value())
			return func() tea.Msg { return promptDoneMsg{id: p.id, value: v} }
		case "esc":
			return func() tea.Msg { return promptDoneMsg{id: p.id, cancel: true} }
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}

func (p *prompt) View() string {
	body := styleBold.Render(p.title) + "\n\n" + p.input.View()
	if p.help != "" {
		body += "\n\n" + styleMuted.Render(p.help)
	}
	return stylePanel.Render(body)
}

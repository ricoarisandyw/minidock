package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"minidock/internal/docker"
)

const maxLogLines = 1000

// detail shows one server: its info, connection string and live logs.
type detail struct {
	inst    docker.Instance
	be      Backend
	vp      viewport.Model
	lines   []string
	stream  *docker.LogStream
	opening bool
	ended   bool
	err     string
	w, h    int
}

func newDetail(inst docker.Instance, be Backend, w, h int) (*detail, tea.Cmd) {
	d := &detail{inst: inst, be: be, w: w, h: h}
	d.vp = viewport.New(w, 5)
	d.layout()
	return d, d.openLogs()
}

func (d *detail) openLogs() tea.Cmd {
	d.opening = true
	d.ended = false
	d.err = ""
	be, id := d.be, d.inst.ID
	return func() tea.Msg {
		s, err := be.Logs(context.Background(), id, 200)
		return logsOpenedMsg{id: id, stream: s, err: err}
	}
}

func waitLine(id string, s *docker.LogStream) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-s.Lines
		return logLineMsg{id: id, line: line, ok: ok}
	}
}

// Close stops following logs.
func (d *detail) Close() {
	d.stream.Close()
	d.stream = nil
}

func (d *detail) headerLines() []string {
	title := stateDot(d.inst) + " " + styleBold.Render(d.inst.Name) + "  " + styleMuted.Render(d.inst.StatusText)
	lines := []string{
		title,
		kv("Template", strings.TrimSpace(d.inst.TemplateID+" "+d.inst.Version)),
		kv("Image", d.inst.Image),
		kv("Port", "localhost:"+itoa(d.inst.HostPort)+"  →  container "+itoa(d.inst.ContainerPort)),
	}
	if d.inst.Volume != "" {
		lines = append(lines, kv("Data", "volume "+d.inst.Volume))
	}
	if d.inst.Connection != "" {
		lines = append(lines, kv("Connect", styleCode.Render(d.inst.Connection)))
	}
	return lines
}

func (d *detail) layout() {
	head := len(d.headerLines()) + 2 // blank line + "Logs" divider
	d.vp.Width = d.w
	d.vp.Height = maxInt(3, d.h-head)
	d.refresh()
}

func (d *detail) resize(w, h int) {
	d.w, d.h = w, h
	d.layout()
}

func (d *detail) refresh() {
	atBottom := d.vp.AtBottom() || len(d.lines) == 0
	cut := make([]string, len(d.lines))
	for i, l := range d.lines {
		cut[i] = trunc(strings.ReplaceAll(l, "\r", ""), d.w)
	}
	d.vp.SetContent(strings.Join(cut, "\n"))
	if atBottom {
		d.vp.GotoBottom()
	}
}

// SetInstance takes a fresh copy of the server (status may have changed).
// It reopens the logs when a stopped server has started again.
func (d *detail) SetInstance(inst docker.Instance) tea.Cmd {
	d.inst = inst
	d.layout()
	if inst.Running() && d.ended && !d.opening {
		d.lines = nil
		return d.openLogs()
	}
	return nil
}

// Update handles keys and log messages.
func (d *detail) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case logsOpenedMsg:
		d.opening = false
		if m.err != nil {
			d.err = docker.Explain(m.err)
			d.ended = true
			return nil
		}
		d.stream = m.stream
		return waitLine(m.id, m.stream)

	case logLineMsg:
		if !m.ok {
			d.ended = true
			d.stream = nil
			return nil
		}
		d.lines = append(d.lines, m.line)
		if over := len(d.lines) - maxLogLines; over > 0 {
			d.lines = d.lines[over:]
		}
		d.refresh()
		if d.stream == nil {
			return nil
		}
		return waitLine(m.id, d.stream)

	case tea.KeyMsg:
		act := func(kind string) tea.Cmd {
			inst := d.inst
			return func() tea.Msg { return serverActionMsg{kind: kind, inst: inst} }
		}
		switch m.String() {
		case "esc", "q":
			d.Close()
			return func() tea.Msg { return detailClosedMsg{} }
		case "s":
			return act("toggle")
		case "R":
			return act("restart")
		case "c":
			return act("copy")
		case "m":
			return act("mcp")
		case "d":
			return act("delete")
		}
	}

	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return cmd
}

// View draws the header and the log viewport.
func (d *detail) View() string {
	var b strings.Builder
	b.WriteString(strings.Join(d.headerLines(), "\n"))
	b.WriteString("\n\n")
	status := "Logs"
	switch {
	case d.err != "":
		status += " — " + d.err
	case d.opening:
		status += " — loading…"
	case d.ended:
		status += " — not following (server is stopped)"
	default:
		status += " — live"
	}
	b.WriteString(styleDivider.Render("── " + status + " "))
	b.WriteString("\n")
	if len(d.lines) == 0 && d.err == "" && !d.opening {
		b.WriteString(styleMuted.Render("No log output yet."))
		return b.String()
	}
	b.WriteString(d.vp.View())
	return b.String()
}

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"minidock/internal/docker"
	"minidock/internal/template"
)

type wizardStage int

const (
	stageForm wizardStage = iota
	stageReview
	stageCreating
	stageDone
	stageFailed
)

// createEvent is a progress update (or the final result) from the creating goroutine.
type createEvent struct {
	progress *docker.Progress
	done     bool
	id       string
	err      error
}

// wizard asks the template's questions, shows a review, then creates the server.
type wizard struct {
	t     template.Template
	be    Backend
	taken []string
	used  map[int]bool

	vals  map[string]*string
	form  *huh.Form
	stage wizardStage
	w, h  int

	spec    template.RunSpec
	preview string
	err     string
	note    string

	events <-chan createEvent
	spin   spinner.Model
	bar    progress.Model
	prog   docker.Progress
}

func newWizard(t template.Template, be Backend, taken []string, used map[int]bool, w, h int) (*wizard, tea.Cmd) {
	wz := &wizard{t: t, be: be, taken: taken, used: used, vals: map[string]*string{}, w: w, h: h}

	set := func(key, val string) { v := val; wz.vals[key] = &v }
	set("name", uniqueName(t.ID, taken))
	set("version", t.DefaultVersion())
	set("port", fmt.Sprint(freePort(t.HostPort(), used)))
	for _, f := range t.Fields {
		set(f.Key, f.Default)
	}

	wz.spin = spinner.New()
	wz.spin.Spinner = spinner.Dot
	wz.spin.Style = lipgloss.NewStyle().Foreground(colorAccent)
	wz.bar = progress.New(progress.WithDefaultGradient(), progress.WithWidth(40), progress.WithoutPercentage())

	return wz, wz.buildForm()
}

func uniqueName(base string, taken []string) string {
	has := func(n string) bool {
		for _, t := range taken {
			if t == n {
				return true
			}
		}
		return false
	}
	name := base
	for i := 2; has(name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// freePort returns the first port at or after want that is not taken.
func freePort(want int, used map[int]bool) int {
	p := want
	for i := 0; i < 200 && p < 65535; i++ {
		if !used[p] && !template.PortInUse(p) {
			return p
		}
		p++
	}
	return want
}

func (wz *wizard) validatePort(s string) error {
	p, err := template.ValidatePort(s)
	if err != nil {
		return err
	}
	if wz.used[p] {
		return fmt.Errorf("port %d is already used by another container", p)
	}
	if template.PortInUse(p) {
		return fmt.Errorf("port %d is already in use on this machine", p)
	}
	return nil
}

// buildForm (re)creates the form from the current answers.
func (wz *wizard) buildForm() tea.Cmd {
	wz.stage = stageForm

	fields := []huh.Field{
		huh.NewInput().
			Title("Server name").
			Description("Also used as the container name").
			Value(wz.vals["name"]).
			Validate(func(s string) error { return template.ValidateInstanceName(strings.TrimSpace(s), wz.taken) }),
	}
	if len(wz.t.Versions) > 0 {
		fields = append(fields, huh.NewSelect[string]().
			Title("Version").
			Options(huh.NewOptions(wz.t.Versions...)...).
			Value(wz.vals["version"]))
	}
	fields = append(fields, huh.NewInput().
		Title("Port on this computer").
		Description(fmt.Sprintf("Forwarded to port %d inside the container", wz.t.ContainerPort)).
		Value(wz.vals["port"]).
		Validate(func(s string) error { return wz.validatePort(s) }))

	for _, f := range wz.t.Fields {
		f := f
		validate := func(s string) error { return template.ValidateFieldValue(f, s) }
		switch f.Type {
		case template.FieldSelect:
			fields = append(fields, huh.NewSelect[string]().
				Title(f.Label).
				Description(f.Help).
				Options(huh.NewOptions(f.Options...)...).
				Value(wz.vals[f.Key]))
		case template.FieldPassword:
			fields = append(fields, huh.NewInput().
				Title(f.Label).
				Description(f.Help).
				EchoMode(huh.EchoModePassword).
				Value(wz.vals[f.Key]).
				Validate(validate))
		default:
			fields = append(fields, huh.NewInput().
				Title(f.Label).
				Description(f.Help).
				Value(wz.vals[f.Key]).
				Validate(validate))
		}
	}

	wz.form = huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(huh.ThemeCharm()).
		WithShowHelp(false).
		WithWidth(minInt(wz.w, 80)).
		WithHeight(maxInt(6, wz.h-3))
	return wz.form.Init()
}

func (wz *wizard) resize(w, h int) {
	wz.w, wz.h = w, h
	if wz.form != nil && wz.stage == stageForm {
		wz.form = wz.form.WithWidth(minInt(w, 80)).WithHeight(maxInt(6, h-3))
	}
}

// collect gathers the answers; passwords keep their spaces.
func (wz *wizard) collect() template.Values {
	v := template.Values{}
	for k, p := range wz.vals {
		v[k] = strings.TrimSpace(*p)
	}
	for _, f := range wz.t.Fields {
		if f.Type == template.FieldPassword {
			v[f.Key] = *wz.vals[f.Key]
		}
	}
	return v
}

func (wz *wizard) enterReview() tea.Cmd {
	vals := wz.collect()
	spec, err := template.Render(wz.t, vals)
	if err != nil {
		wz.err = err.Error()
		return wz.buildForm()
	}
	wz.err = ""
	wz.spec = spec
	wz.preview = spec.Preview()
	wz.stage = stageReview
	return nil
}

func closeWizard(created bool) tea.Cmd {
	return func() tea.Msg { return wizardClosedMsg{created: created} }
}

func waitCreate(ch <-chan createEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return createEventMsg{createEvent{done: true, err: errors.New("creation stopped unexpectedly")}}
		}
		return createEventMsg{ev}
	}
}

func (wz *wizard) startCreate() tea.Cmd {
	ch := make(chan createEvent, 32)
	wz.events = ch
	wz.stage = stageCreating
	wz.prog = docker.Progress{Status: "Starting…", Percent: -1}
	spec, be := wz.spec, wz.be

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		id, err := be.Create(ctx, spec, func(p docker.Progress) {
			select {
			case ch <- createEvent{progress: &p}:
			default: // the screen is behind; skipping an update is fine
			}
		})
		ch <- createEvent{done: true, id: id, err: err}
		close(ch)
	}()
	return tea.Batch(wz.spin.Tick, waitCreate(ch))
}

// Update handles messages for the wizard and returns the next command.
func (wz *wizard) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case createEventMsg:
		ev := m.ev
		switch {
		case ev.progress != nil:
			wz.prog = *ev.progress
			return waitCreate(wz.events)
		case ev.done && ev.err != nil:
			wz.stage = stageFailed
			wz.err = docker.Explain(ev.err)
		case ev.done:
			wz.stage = stageDone
		}
		return nil

	case spinner.TickMsg:
		if wz.stage != stageCreating {
			return nil
		}
		var cmd tea.Cmd
		wz.spin, cmd = wz.spin.Update(msg)
		return cmd

	case tea.KeyMsg:
		switch wz.stage {
		case stageForm:
			if m.String() == "esc" {
				return closeWizard(false)
			}
		case stageReview:
			switch m.String() {
			case "enter", "y":
				return wz.startCreate()
			case "esc":
				return wz.buildForm()
			}
			return nil
		case stageCreating:
			return nil
		case stageDone:
			switch m.String() {
			case "c":
				if err := copyToClipboard(wz.spec.Connection); err != nil {
					wz.note = "Could not copy: " + err.Error()
				} else {
					wz.note = "Connection string copied."
				}
			case "enter", "esc":
				return closeWizard(true)
			}
			return nil
		case stageFailed:
			switch m.String() {
			case "enter", "esc":
				wz.err = ""
				wz.stage = stageReview
			}
			return nil
		}
	}

	if wz.stage != stageForm {
		return nil
	}
	model, cmd := wz.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		wz.form = f
	}
	switch wz.form.State {
	case huh.StateCompleted:
		return tea.Batch(cmd, wz.enterReview())
	case huh.StateAborted:
		return closeWizard(false)
	}
	return cmd
}

// Hints returns the key hints for the current stage.
func (wz *wizard) Hints() []hint {
	switch wz.stage {
	case stageReview:
		return hintsReview
	case stageCreating:
		return nil
	case stageDone:
		return hintsDone
	case stageFailed:
		return hintsFailed
	}
	return hintsWizard
}

// View draws the current step.
func (wz *wizard) View() string {
	title := styleTitle.Render("New server  ") + styleBold.Render(wz.t.Title())
	var b strings.Builder
	b.WriteString(title + "\n\n")

	switch wz.stage {
	case stageForm:
		if wz.err != "" {
			b.WriteString(styleErr.Render(wz.err) + "\n\n")
		}
		b.WriteString(wz.form.View())

	case stageReview:
		b.WriteString(styleBold.Render("Review") + styleMuted.Render("  This is what MiniDock will run:") + "\n\n")
		b.WriteString(stylePanel.Render(styleCode.Render(wz.preview)) + "\n\n")
		if wz.spec.Volume != "" {
			b.WriteString(styleMuted.Render("Your data lives in the Docker volume "+wz.spec.Volume+", so it survives deleting the container.") + "\n")
		}
		b.WriteString(styleMuted.Render("The server only listens on 127.0.0.1 (this computer)."))

	case stageCreating:
		b.WriteString(wz.spin.View() + " " + wz.prog.Status + "\n\n")
		if wz.prog.Percent >= 0 {
			b.WriteString(wz.bar.ViewAs(wz.prog.Percent) + fmt.Sprintf(" %3.0f%%", wz.prog.Percent*100) + "\n")
		}
		b.WriteString("\n" + styleMuted.Render("The first time, Docker downloads the image. Please wait…"))

	case stageDone:
		b.WriteString(styleOK.Render(fmt.Sprintf("✔ %s is running on localhost:%d", wz.spec.Name, wz.spec.HostPort)) + "\n\n")
		if wz.spec.Connection != "" {
			b.WriteString(styleMuted.Render("Connection string") + "\n")
			b.WriteString(stylePanel.Render(styleCode.Render(wz.spec.Connection)) + "\n")
		}
		if wz.note != "" {
			b.WriteString("\n" + styleMuted.Render(wz.note))
		}

	case stageFailed:
		b.WriteString(styleErr.Render("✘ Could not create the server") + "\n\n")
		b.WriteString(lipgloss.NewStyle().Width(maxInt(20, minInt(wz.w, 80))).Render(wz.err))
	}
	return b.String()
}

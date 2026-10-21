package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"minidock/internal/template"
)

// editorFields holds the raw text of each editor input.
type editorFields struct {
	id, name, icon, desc, image, versions string
	cport, hport, dataPath, command       string
	env, fields, conn                     string
}

// editor is a three-page form to create or edit a custom template.
type editor struct {
	isNew bool
	taken []string // ids already in use (new templates must avoid them)
	f     editorFields
	form  *huh.Form
	err   string
	w, h  int
}

// newEditor opens the editor. base prefills the form; for a new template it may be empty.
func newEditor(base template.Template, isNew bool, taken []string, w, h int) (*editor, tea.Cmd) {
	e := &editor{isNew: isNew, taken: taken, w: w, h: h}
	e.f = editorFields{
		id:       base.ID,
		name:     base.Name,
		icon:     base.Icon,
		desc:     base.Description,
		image:    base.Image,
		versions: template.VersionsText(base.Versions),
		dataPath: base.DataPath,
		command:  template.JoinCommand(base.Command),
		env:      template.EnvText(base.Env),
		fields:   template.FieldsText(base.Fields),
		conn:     base.ConnectionString,
	}
	if base.ContainerPort > 0 {
		e.f.cport = strconv.Itoa(base.ContainerPort)
	}
	if base.DefaultHostPort > 0 {
		e.f.hport = strconv.Itoa(base.DefaultHostPort)
	}
	return e, e.buildForm()
}

func (e *editor) buildForm() tea.Cmd {
	required := func(label string) func(string) error {
		return func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		}
	}
	portOK := func(optional bool) func(string) error {
		return func(s string) error {
			s = strings.TrimSpace(s)
			if s == "" && optional {
				return nil
			}
			_, err := template.ValidatePort(s)
			return err
		}
	}

	idField := huh.Field(huh.NewNote().Title("Template id: " + e.f.id).Description("The id cannot change after saving."))
	if e.isNew {
		idField = huh.NewInput().
			Title("Template id").
			Description("Lowercase, used as the file name (e.g. my-nginx)").
			Value(&e.f.id).
			Validate(func(s string) error {
				s = strings.TrimSpace(s)
				if err := template.ValidateID(s); err != nil {
					return err
				}
				for _, t := range e.taken {
					if t == s {
						return fmt.Errorf("id %q is already used", s)
					}
				}
				return nil
			})
	}

	basics := huh.NewGroup(
		huh.NewInput().Title("Name").Value(&e.f.name).Validate(required("name")),
		idField,
		huh.NewInput().Title("Icon").Description("One emoji, optional").Value(&e.f.icon).CharLimit(4),
		huh.NewInput().Title("Description").Value(&e.f.desc),
		huh.NewInput().Title("Docker image").Description("For example: nginx, postgres, ghcr.io/org/app").Value(&e.f.image).Validate(required("image")),
		huh.NewInput().Title("Versions (tags)").Description("Comma separated, first one is the default. Leave empty for the image default.").Value(&e.f.versions),
	).Title("1/3  Basics")

	network := huh.NewGroup(
		huh.NewInput().Title("Container port").Description("The port the program listens on inside the container").Value(&e.f.cport).Validate(portOK(false)),
		huh.NewInput().Title("Suggested port on this computer").Description("Optional, defaults to the container port").Value(&e.f.hport).Validate(portOK(true)),
		huh.NewInput().Title("Data folder in container").Description("Optional. Mounted as a Docker volume so data is kept").Value(&e.f.dataPath),
		huh.NewInput().Title("Start command").Description(`Optional. Use "quotes" for arguments with spaces`).Value(&e.f.command),
	).Title("2/3  Port & data")

	setup := huh.NewGroup(
		huh.NewText().Title("Environment variables").Description("KEY=VALUE per line. Use {{key}} to insert wizard answers").Lines(3).Value(&e.f.env),
		huh.NewText().Title("Wizard questions").Description(template.FieldsHelp).Lines(3).Value(&e.f.fields),
		huh.NewInput().Title("Connection string").Description("Optional, e.g. mysql://root:{{password}}@{{host}}:{{port}}/db").Value(&e.f.conn),
	).Title("3/3  Questions & environment")

	e.form = huh.NewForm(basics, network, setup).
		WithTheme(huh.ThemeCharm()).
		WithShowHelp(false).
		WithWidth(minInt(e.w, 90)).
		WithHeight(maxInt(8, e.h-3))
	return e.form.Init()
}

func (e *editor) resize(w, h int) {
	e.w, e.h = w, h
	if e.form != nil {
		e.form = e.form.WithWidth(minInt(w, 90)).WithHeight(maxInt(8, h-3))
	}
}

func (e *editor) build() (template.Template, error) {
	cport, err := strconv.Atoi(strings.TrimSpace(e.f.cport))
	if err != nil {
		return template.Template{}, fmt.Errorf("container port must be a number")
	}
	hport := 0
	if s := strings.TrimSpace(e.f.hport); s != "" {
		if hport, err = strconv.Atoi(s); err != nil {
			return template.Template{}, fmt.Errorf("suggested port must be a number")
		}
	}
	env, err := template.ParseEnv(e.f.env)
	if err != nil {
		return template.Template{}, err
	}
	fields, err := template.ParseFields(e.f.fields)
	if err != nil {
		return template.Template{}, err
	}
	t := template.Template{
		ID:               strings.TrimSpace(e.f.id),
		Name:             strings.TrimSpace(e.f.name),
		Icon:             strings.TrimSpace(e.f.icon),
		Description:      strings.TrimSpace(e.f.desc),
		Image:            strings.TrimSpace(e.f.image),
		Versions:         template.ParseVersions(e.f.versions),
		ContainerPort:    cport,
		DefaultHostPort:  hport,
		DataPath:         strings.TrimSpace(e.f.dataPath),
		Fields:           fields,
		Env:              env,
		Command:          template.SplitCommand(e.f.command),
		ConnectionString: strings.TrimSpace(e.f.conn),
	}
	return t, template.ValidateTemplate(t)
}

// Update forwards messages to the form and finishes when it is completed or cancelled.
func (e *editor) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		return func() tea.Msg { return editorDoneMsg{} }
	}
	model, cmd := e.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		e.form = f
	}
	switch e.form.State {
	case huh.StateCompleted:
		t, err := e.build()
		if err != nil {
			e.err = err.Error()
			return tea.Batch(cmd, e.buildForm())
		}
		isNew := e.isNew
		return func() tea.Msg { return editorDoneMsg{tpl: t, isNew: isNew, saved: true} }
	case huh.StateAborted:
		return func() tea.Msg { return editorDoneMsg{} }
	}
	return cmd
}

// View draws the form with the latest error on top.
func (e *editor) View() string {
	title := "Edit template"
	if e.isNew {
		title = "New template"
	}
	out := styleTitle.Render(title) + "\n\n"
	if e.err != "" {
		out += styleErr.Render(e.err) + "\n\n"
	}
	return out + e.form.View()
}

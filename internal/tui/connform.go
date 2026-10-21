package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"minidock/internal/docker"
	"minidock/internal/external"
)

// connFields holds the raw text of each input in the connection form.
type connFields struct {
	name, container, client string
	user, password, db      string
	note                    string
}

// connForm asks for everything needed to read an existing database:
// which container, and the login to use inside it.
type connForm struct {
	isNew      bool
	taken      []string // names already in use (new connections must avoid them)
	candidates []docker.Foreign
	f          connFields
	form       *huh.Form
	err        string
	w, h       int
}

// newConnForm opens the form. base prefills it; for a new connection it is empty.
// candidates are the containers MiniDock did not create, newest and most likely first.
func newConnForm(base external.Conn, isNew bool, taken []string, candidates []docker.Foreign, w, h int) (*connForm, tea.Cmd) {
	c := &connForm{isNew: isNew, taken: taken, candidates: candidates, w: w, h: h}
	c.f = connFields{
		name:      base.Name,
		container: base.Container,
		client:    base.Client,
		user:      base.User,
		password:  base.Password,
		db:        base.Database,
		note:      base.Note,
	}
	// A new connection starts on the first container that looks like a database.
	if c.f.container == "" {
		for _, f := range candidates {
			if f.Client != "" && f.Running() {
				c.f.container, c.f.client = f.Name, f.Client
				if c.f.name == "" {
					c.f.name = f.Name
				}
				break
			}
		}
	}
	if c.f.client == "" {
		c.f.client = "mysql"
	}
	if c.f.user == "" {
		c.f.user = defaultUserFor(c.f.client)
	}
	return c, c.buildForm()
}

// defaultUserFor is the superuser each image creates by default, which is the
// one a person is most likely to start from.
func defaultUserFor(client string) string {
	if client == "psql" {
		return "postgres"
	}
	return "root"
}

// containerOptions turns the scanned containers into pick-list entries.
// Containers that do not look like MySQL/MariaDB are kept but marked, because
// the image name is only a guess — a custom image may still hold a database.
func (c *connForm) containerOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.candidates)+1)
	for _, f := range c.candidates {
		label := f.Name
		if f.Compose != "" {
			label += "  (" + f.Compose + ")"
		}
		label += "  " + f.Image
		if !f.Running() {
			label += "  — stopped"
		} else if f.Client == "" {
			label += "  — not a database image?"
		}
		opts = append(opts, huh.NewOption(label, f.Name))
	}
	// Keep a saved container that is no longer on this machine selectable,
	// so editing a connection does not silently repoint it.
	if c.f.container != "" {
		for _, o := range opts {
			if o.Value == c.f.container {
				return opts
			}
		}
		opts = append(opts, huh.NewOption(c.f.container+"  — not found right now", c.f.container))
	}
	return opts
}

// clientFor returns the SQL client guessed for a container name, or "".
func (c *connForm) clientFor(container string) string {
	for _, f := range c.candidates {
		if f.Name == container {
			return f.Client
		}
	}
	return ""
}

func (c *connForm) buildForm() tea.Cmd {
	opts := c.containerOptions()
	if len(opts) == 0 {
		// Nothing to choose: say so in the form rather than showing an empty list.
		c.form = huh.NewForm(huh.NewGroup(
			huh.NewNote().
				Title("No other containers found").
				Description("MiniDock looks for Docker containers it did not create.\n" +
					"Start the database you want to read (for example with docker compose up),\n" +
					"then press esc and n again."),
		)).WithTheme(huh.ThemeCharm()).WithShowHelp(false).
			WithWidth(minInt(c.w, 90)).WithHeight(maxInt(8, c.h-3))
		return c.form.Init()
	}

	required := func(label string) func(string) error {
		return func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		}
	}

	where := huh.NewGroup(
		huh.NewSelect[string]().
			Title("Container").
			Description("The database container to read. It is looked up by name each time,\nso recreating it (docker compose up) keeps working.").
			Options(opts...).
			Value(&c.f.container),
		huh.NewSelect[string]().
			Title("SQL client in the container").
			Description("The program MiniDock runs inside it: psql for PostgreSQL, mariadb for MariaDB images.").
			Options(
				huh.NewOption("mysql (MySQL)", "mysql"),
				huh.NewOption("mariadb (MariaDB)", "mariadb"),
				huh.NewOption("psql (PostgreSQL)", "psql"),
			).
			Value(&c.f.client),
	).Title("1/2  Which database")

	who := huh.NewGroup(
		huh.NewInput().Title("Name for this connection").
			Description("Used as `minidock mcp --server <name>`. Letters, digits, dot, dash, underscore.").
			Value(&c.f.name).
			Validate(func(s string) error {
				s = strings.TrimSpace(s)
				cand := external.Conn{Name: s, Container: "x", Client: "mysql", User: "x"}
				if err := external.Validate(cand); err != nil {
					return err
				}
				for _, t := range c.taken {
					if t == s {
						return fmt.Errorf("a connection named %q already exists", s)
					}
				}
				return nil
			}),
		huh.NewInput().Title("User").Description("A user that may read the data. A read-only user is a good idea.").
			Value(&c.f.user).Validate(required("user")),
		huh.NewInput().Title("Password").Description("Stored in a file only you can read. Leave empty if there is none.").
			EchoMode(huh.EchoModePassword).Value(&c.f.password),
		huh.NewInput().Title("Default database").Description("Optional. Queries without a database name use this one.").Value(&c.f.db),
		huh.NewInput().Title("Note").Description("Optional reminder, shown in the list.").Value(&c.f.note),
	).Title("2/2  Login")

	c.form = huh.NewForm(where, who).
		WithTheme(huh.ThemeCharm()).
		WithShowHelp(false).
		WithWidth(minInt(c.w, 90)).
		WithHeight(maxInt(8, c.h-3))
	return c.form.Init()
}

func (c *connForm) resize(w, h int) {
	c.w, c.h = w, h
	if c.form != nil {
		c.form = c.form.WithWidth(minInt(w, 90)).WithHeight(maxInt(8, h-3))
	}
}

func (c *connForm) build() (external.Conn, error) {
	conn := external.Conn{
		Name:      strings.TrimSpace(c.f.name),
		Container: strings.TrimSpace(c.f.container),
		Client:    strings.TrimSpace(c.f.client),
		User:      strings.TrimSpace(c.f.user),
		Password:  c.f.password,
		Database:  strings.TrimSpace(c.f.db),
		Note:      strings.TrimSpace(c.f.note),
	}
	return conn, external.Validate(conn)
}

// Update forwards messages to the form and finishes when it completes or is cancelled.
func (c *connForm) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		return func() tea.Msg { return connFormDoneMsg{} }
	}
	model, cmd := c.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		c.form = f
	}
	switch c.form.State {
	case huh.StateCompleted:
		conn, err := c.build()
		if err != nil {
			c.err = err.Error()
			return tea.Batch(cmd, c.buildForm())
		}
		isNew := c.isNew
		return func() tea.Msg { return connFormDoneMsg{conn: conn, isNew: isNew, saved: true} }
	case huh.StateAborted:
		return func() tea.Msg { return connFormDoneMsg{} }
	}
	return cmd
}

// View draws the form with the latest error on top, plus a hint when the chosen
// container does not look like the client that is selected.
func (c *connForm) View() string {
	title := "Edit connection"
	if c.isNew {
		title = "Read an existing database"
	}
	out := styleTitle.Render(title) + "\n\n"
	if c.err != "" {
		out += styleErr.Render(c.err) + "\n\n"
	}
	if guess := c.clientFor(c.f.container); guess != "" && guess != c.f.client {
		out += styleWarn.Render("That container looks like a "+guess+" image, but "+c.f.client+" is selected.") + "\n\n"
	}
	return out + c.form.View()
}

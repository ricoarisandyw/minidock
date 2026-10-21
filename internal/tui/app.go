package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"minidock/internal/docker"
	"minidock/internal/external"
	"minidock/internal/mcpserver"
	"minidock/internal/template"
)

type tabID int

const (
	tabTemplates tabID = iota
	tabServers
	tabExternal
	tabMine
	tabCount
)

var tabNames = [tabCount]string{"Templates", "Servers", "External", "My Templates"}

type mode int

const (
	modeBrowse mode = iota
	modeWizard
	modeDetail
	modeEditor
	modeConfirm
	modePrompt
	modeConnForm
)

const refreshEvery = 3 * time.Second

// App is the root Bubble Tea model.
type App struct {
	be    Backend
	store *template.Store
	conns *external.Store

	width, height int
	tab           tabID
	mode          mode
	showHelp      bool

	status      docker.Status
	statusKnown bool

	toast    string
	toastErr bool
	toastSeq int

	problems    []error
	all, mine   gallery
	servers     serversView
	ext         externalView
	extProblems []error
	foreign     []docker.Foreign
	usedPorts   map[int]bool
	instances   []docker.Instance

	wiz    *wizard
	det    *detail
	ed     *editor
	cf     *connForm
	conf   *confirm
	prompt *prompt

	pendingInst docker.Instance
	pendingTpl  template.Template
	pendingConn external.Conn
	// pendingMCPName is the server or connection the tool dialog is about.
	pendingMCPName string
}

// New builds the app around a Docker backend, a template store and a store of
// connections to databases MiniDock does not run.
func New(be Backend, store *template.Store, conns *external.Store) *App {
	a := &App{
		be:    be,
		store: store,
		conns: conns,
		all:   newGallery("No templates found."),
		mine: newGallery("You have no custom templates yet.\n\n" +
			"Press n to create one, u to duplicate a template,\nor i to import a JSON file."),
	}
	a.reloadTemplates()
	a.reloadConns()
	return a
}

// reloadConns re-reads the saved connections from disk.
func (a *App) reloadConns() {
	if a.conns == nil {
		return
	}
	items, problems := a.conns.List()
	a.extProblems = problems
	a.ext.SetItems(items)
	a.ext.SetLive(a.foreign)
}

func (a *App) reloadTemplates() {
	items, problems := a.store.List()
	a.problems = problems
	a.all.SetItems(items)
	var custom []template.Template
	for _, t := range items {
		if !t.Builtin {
			custom = append(custom, t)
		}
	}
	a.mine.SetItems(custom)
}

// Init starts the first Docker checks and the refresh timer.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.checkStatus(), a.refreshInstances(), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) checkStatus() tea.Cmd {
	be := a.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return statusMsg{status: be.Ping(ctx)}
	}
}

func (a *App) refreshInstances() tea.Cmd {
	be := a.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		list, err := be.List(ctx)
		if err != nil {
			return instancesMsg{err: err}
		}
		used, _ := be.UsedPorts(ctx)
		return instancesMsg{list: list, used: used}
	}
}

// refreshForeign scans for containers MiniDock did not create, so the pick list
// in the connection form is current. A failure is not worth a toast: the form says so.
func (a *App) refreshForeign() tea.Cmd {
	be := a.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		list, err := be.ListForeign(ctx)
		return foreignMsg{list: list, err: err}
	}
}

// act runs a Docker action in the background and reports back with actionMsg.
func (a *App) act(label string, fn func(ctx context.Context, be Backend) error) tea.Cmd {
	be := a.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return actionMsg{label: label, err: fn(ctx, be)}
	}
}

func (a *App) setToast(text string, isErr bool) tea.Cmd {
	a.toastSeq++
	seq := a.toastSeq
	a.toast, a.toastErr = text, isErr
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return toastClearMsg{seq: seq} })
}

// Update is the main message handler.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		a.resizeChildren()
		return a, nil

	case tickMsg:
		return a, tea.Batch(a.checkStatus(), a.refreshInstances(), a.refreshForeign(), tickCmd())

	case statusMsg:
		a.status, a.statusKnown = m.status, true
		return a, nil

	case instancesMsg:
		return a, a.onInstances(m)

	case actionMsg:
		if m.err != nil {
			return a, tea.Batch(a.setToast(docker.Explain(m.err), true), a.refreshInstances())
		}
		return a, tea.Batch(a.setToast(m.label, false), a.refreshInstances())

	case toastClearMsg:
		if m.seq == a.toastSeq {
			a.toast = ""
		}
		return a, nil

	case foreignMsg:
		if m.err == nil {
			a.foreign = m.list
			a.ext.SetLive(m.list)
		}
		return a, nil

	case serverActionMsg:
		return a, a.onServerAction(m)

	case externalActionMsg:
		return a, a.onExternalAction(m)

	case connFormDoneMsg:
		return a, a.onConnFormDone(m)

	case connTestMsg:
		if m.err != nil {
			return a, a.setToast("Could not read "+m.name+": "+m.err.Error(), true)
		}
		return a, a.setToast(m.summary, false)

	case confirmChoiceMsg:
		return a, a.onConfirm(m)

	case promptDoneMsg:
		return a, a.onPrompt(m)

	case wizardClosedMsg:
		a.wiz = nil
		a.mode = modeBrowse
		if m.created {
			a.tab = tabServers
			return a, tea.Batch(a.setToast("Server created", false), a.refreshInstances())
		}
		return a, nil

	case detailClosedMsg:
		a.det = nil
		a.mode = modeBrowse
		return a, nil

	case editorDoneMsg:
		return a, a.onEditorDone(m)

	case logsOpenedMsg:
		if a.det == nil || a.det.inst.ID != m.id {
			m.stream.Close() // the detail view was closed while the logs were opening
			return a, nil
		}
		return a, a.det.Update(m)

	case logLineMsg:
		if a.det == nil || a.det.inst.ID != m.id {
			return a, nil
		}
		return a, a.det.Update(m)

	case tea.KeyMsg:
		return a.onKey(m)
	}

	// Everything else (cursor blinks, form-internal messages, spinner ticks,
	// creation progress) belongs to whichever screen is open.
	return a, a.routeToChild(msg)
}

func (a *App) routeToChild(msg tea.Msg) tea.Cmd {
	switch a.mode {
	case modeWizard:
		if a.wiz != nil {
			return a.wiz.Update(msg)
		}
	case modeDetail:
		if a.det != nil {
			return a.det.Update(msg)
		}
	case modeEditor:
		if a.ed != nil {
			return a.ed.Update(msg)
		}
	case modePrompt:
		if a.prompt != nil {
			return a.prompt.Update(msg)
		}
	case modeConnForm:
		if a.cf != nil {
			return a.cf.Update(msg)
		}
	}
	return nil
}

func (a *App) onInstances(m instancesMsg) tea.Cmd {
	a.servers.SetItems(m.list, m.err)
	if m.err != nil {
		return nil
	}
	a.instances, a.usedPorts = m.list, m.used
	if a.det == nil {
		return nil
	}
	if inst, ok := a.servers.byID(a.det.inst.ID); ok {
		return a.det.SetInstance(inst)
	}
	a.closeDetail()
	a.mode = modeBrowse
	return a.setToast("That server no longer exists", true)
}

func (a *App) closeDetail() {
	if a.det != nil {
		a.det.Close()
		a.det = nil
	}
}

// ---------------------------------------------------------------- keys

func (a *App) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		a.closeDetail()
		return a, tea.Quit
	}
	if a.showHelp {
		a.showHelp = false
		return a, nil
	}
	switch a.mode {
	case modeWizard:
		return a, a.wiz.Update(k)
	case modeDetail:
		return a, a.det.Update(k)
	case modeEditor:
		return a, a.ed.Update(k)
	case modeConfirm:
		return a, a.conf.Update(k)
	case modePrompt:
		return a, a.prompt.Update(k)
	case modeConnForm:
		return a, a.cf.Update(k)
	}
	return a.onBrowseKey(k)
}

func (a *App) activeGallery() *gallery {
	switch a.tab {
	case tabTemplates:
		return &a.all
	case tabMine:
		return &a.mine
	}
	return nil
}

func (a *App) onBrowseKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	g := a.activeGallery()
	if g != nil && g.Filtering() {
		_, cmd := g.HandleKey(k)
		return a, cmd
	}

	switch k.String() {
	case "q":
		return a, tea.Quit
	case "tab", "right", "l":
		a.tab = (a.tab + 1) % tabCount
		return a, nil
	case "shift+tab", "left", "h":
		a.tab = (a.tab + tabCount - 1) % tabCount
		return a, nil
	case "?":
		a.showHelp = true
		return a, nil
	case "r":
		a.statusKnown = false
		a.reloadTemplates()
		a.reloadConns()
		return a, tea.Batch(a.checkStatus(), a.refreshInstances(), a.refreshForeign())
	}

	if g != nil {
		return a, a.onTemplateKey(g, k)
	}
	if a.tab == tabExternal {
		_, cmd := a.ext.HandleKey(k)
		return a, cmd
	}
	handled, open, cmd := a.servers.HandleKey(k)
	if open {
		if inst, ok := a.servers.selected(); ok {
			return a, a.openDetail(inst)
		}
	}
	_ = handled
	return a, cmd
}

func (a *App) onTemplateKey(g *gallery, k tea.KeyMsg) tea.Cmd {
	if handled, cmd := g.HandleKey(k); handled {
		return cmd
	}
	sel, has := g.selected()
	switch k.String() {
	case "enter":
		if has {
			return a.openWizard(sel)
		}
	case "n":
		return a.openEditor(template.Template{}, true)
	case "u":
		if has {
			dup := sel
			dup.Builtin = false
			dup.ID = uniqueName(sel.ID+"-copy", a.store.IDs())
			dup.Name = sel.Name + " (copy)"
			return a.openEditor(dup, true)
		}
	case "e":
		if !has {
			return nil
		}
		if sel.Builtin {
			return a.setToast("Built-in templates can't be edited. Press u to duplicate it first.", true)
		}
		return a.openEditor(sel, false)
	case "d":
		if !has {
			return nil
		}
		if sel.Builtin {
			return a.setToast("Built-in templates can't be deleted.", true)
		}
		a.pendingTpl = sel
		a.conf = &confirm{
			id:    "delete-template",
			title: fmt.Sprintf("Delete template %q?", sel.Name),
			body:  "Servers already created from it keep running. Only the template file is removed.",
			opts:  []confirmOpt{{key: "y", label: "Delete template", value: "yes"}},
		}
		a.mode = modeConfirm
	case "x":
		if has {
			a.pendingTpl = sel
			return a.openPrompt("export", "Export “"+sel.Name+"” to a JSON file", "Enter a file path. ~ means your home folder.", "./"+sel.ID+".json")
		}
	case "i":
		return a.openPrompt("import", "Import a template", "Enter the path of a template JSON file.", "")
	}
	return nil
}

// ---------------------------------------------------------------- screens

func (a *App) openWizard(t template.Template) tea.Cmd {
	if a.statusKnown && !a.status.Connected {
		return a.setToast("Docker is not running. Start it first, then press r.", true)
	}
	taken := make([]string, 0, len(a.instances))
	for _, i := range a.instances {
		taken = append(taken, i.Name)
	}
	w, cmd := newWizard(t, a.be, taken, a.usedPorts, a.width, a.bodyHeight())
	a.wiz = w
	a.mode = modeWizard
	return cmd
}

func (a *App) openDetail(inst docker.Instance) tea.Cmd {
	d, cmd := newDetail(inst, a.be, a.width, a.bodyHeight())
	a.det = d
	a.mode = modeDetail
	return cmd
}

func (a *App) openEditor(base template.Template, isNew bool) tea.Cmd {
	e, cmd := newEditor(base, isNew, a.store.IDs(), a.width, a.bodyHeight())
	a.ed = e
	a.mode = modeEditor
	return cmd
}

func (a *App) openPrompt(id, title, help, initial string) tea.Cmd {
	p, cmd := newPrompt(id, title, help, initial)
	a.prompt = p
	a.mode = modePrompt
	return cmd
}

func (a *App) backFromModal() {
	a.conf, a.prompt = nil, nil
	a.mode = modeBrowse
	if a.det != nil {
		a.mode = modeDetail
	}
}

func (a *App) onEditorDone(m editorDoneMsg) tea.Cmd {
	a.ed = nil
	a.mode = modeBrowse
	if !m.saved {
		return nil
	}
	if err := a.store.Save(m.tpl); err != nil {
		return a.setToast("Could not save template: "+err.Error(), true)
	}
	a.reloadTemplates()
	a.mine.selectID(m.tpl.ID)
	a.all.selectID(m.tpl.ID)
	a.tab = tabMine
	return a.setToast("Saved template “"+m.tpl.Name+"”", false)
}

func (a *App) onPrompt(m promptDoneMsg) tea.Cmd {
	a.backFromModal()
	if m.cancel || m.value == "" {
		return nil
	}
	switch m.id {
	case "export":
		if err := a.store.Export(a.pendingTpl.ID, m.value); err != nil {
			return a.setToast("Export failed: "+err.Error(), true)
		}
		path, err := filepath.Abs(m.value)
		if err != nil {
			path = m.value
		}
		return a.setToast("Exported to "+path, false)
	case "import":
		t, err := a.store.Import(m.value)
		if err != nil {
			return a.setToast("Import failed: "+err.Error(), true)
		}
		a.reloadTemplates()
		a.mine.selectID(t.ID)
		a.tab = tabMine
		return a.setToast("Imported “"+t.Name+"”", false)
	}
	return nil
}

func (a *App) onConfirm(m confirmChoiceMsg) tea.Cmd {
	a.backFromModal()
	if m.value == "" {
		return nil
	}
	switch m.id {
	case "mcp-tool":
		return a.copyMCPCommand(m.value)
	case "delete-template":
		if err := a.store.Delete(a.pendingTpl.ID); err != nil {
			return a.setToast(err.Error(), true)
		}
		a.reloadTemplates()
		return a.setToast("Deleted template “"+a.pendingTpl.Name+"”", false)
	case "delete-conn":
		if err := a.conns.Delete(a.pendingConn.Name); err != nil {
			return a.setToast(err.Error(), true)
		}
		a.reloadConns()
		return a.setToast("Removed connection “"+a.pendingConn.Name+"”", false)
	case "delete-server":
		inst := a.pendingInst
		removeData := m.value == "data"
		a.closeDetail()
		a.mode = modeBrowse
		return a.act("Deleted "+inst.Name, func(ctx context.Context, be Backend) error {
			return be.Remove(ctx, inst, removeData)
		})
	}
	return nil
}

func (a *App) onServerAction(m serverActionMsg) tea.Cmd {
	inst := m.inst
	switch m.kind {
	case "toggle":
		if inst.Running() {
			return a.act("Stopped "+inst.Name, func(ctx context.Context, be Backend) error { return be.Stop(ctx, inst.ID) })
		}
		return a.act("Started "+inst.Name, func(ctx context.Context, be Backend) error { return be.Start(ctx, inst.ID) })
	case "restart":
		return a.act("Restarted "+inst.Name, func(ctx context.Context, be Backend) error { return be.Restart(ctx, inst.ID) })
	case "copy":
		if inst.Connection == "" {
			return a.setToast("This server has no connection string.", true)
		}
		if err := copyToClipboard(inst.Connection); err != nil {
			return a.setToast("Could not copy ("+err.Error()+"). Connection: "+inst.Connection, true)
		}
		return a.setToast("Connection string copied", false)
	case "mcp":
		if inst.SQLClient() == "" {
			return a.setToast("MCP is only available for MySQL, MariaDB and PostgreSQL servers.", true)
		}
		return a.askWhichAITool(inst.Name)
	case "delete":
		a.pendingInst = inst
		body := "The container will be removed."
		opts := []confirmOpt{{key: "y", label: "Delete container, keep data", value: "container"}}
		if inst.Volume != "" {
			body += " Your data is stored in the volume " + inst.Volume + ". Keep it to reuse the data later, or delete it too."
			opts = append(opts, confirmOpt{key: "d", label: "Delete container AND data (cannot be undone)", value: "data"})
		}
		a.conf = &confirm{id: "delete-server", title: fmt.Sprintf("Delete server %q?", inst.Name), body: body, opts: opts}
		a.mode = modeConfirm
	}
	return nil
}

// ---------------------------------------------------------------- external connections

func (a *App) openConnForm(base external.Conn, isNew bool) tea.Cmd {
	if a.conns == nil {
		return a.setToast("Connections cannot be saved: no config folder.", true)
	}
	taken := a.conns.Names()
	if !isNew {
		// Editing keeps its own name available.
		kept := taken[:0]
		for _, n := range taken {
			if n != base.Name {
				kept = append(kept, n)
			}
		}
		taken = kept
	}
	c, cmd := newConnForm(base, isNew, taken, a.foreign, a.width, a.bodyHeight())
	a.cf = c
	a.mode = modeConnForm
	return cmd
}

func (a *App) onExternalAction(m externalActionMsg) tea.Cmd {
	switch m.kind {
	case "new":
		if a.statusKnown && !a.status.Connected {
			return a.setToast("Docker is not running, so there are no containers to choose. Start it, then press r.", true)
		}
		return a.openConnForm(external.Conn{}, true)
	case "edit":
		a.pendingConn = m.conn
		return a.openConnForm(m.conn, false)
	case "mcp":
		return a.askWhichAITool(m.conn.Name)
	case "test":
		return a.testConn(m.conn)
	case "toggle", "restart":
		return a.controlConnContainer(m.kind, m.conn)
	case "delete":
		a.pendingConn = m.conn
		a.conf = &confirm{
			id:    "delete-conn",
			title: fmt.Sprintf("Remove connection %q?", m.conn.Name),
			body: "Only the saved login is removed. The container " + m.conn.Container +
				" and its data are left untouched.",
			opts: []confirmOpt{{key: "y", label: "Remove connection", value: "yes"}},
		}
		a.mode = modeConfirm
	}
	return nil
}

// controlConnContainer starts, stops or restarts the container behind a saved
// connection. MiniDock does not own that container, so it says whose it is in the
// message, and refuses when it cannot find it rather than guessing.
func (a *App) controlConnContainer(kind string, c external.Conn) tea.Cmd {
	f, ok := a.ext.container(c)
	if !ok {
		return a.setToast("No container named "+c.Container+" right now. Start it yourself, or edit the connection.", true)
	}
	switch {
	case kind == "restart":
		return a.act("Restarted "+f.Name, func(ctx context.Context, be Backend) error { return be.Restart(ctx, f.ID) })
	case f.Running():
		return a.act("Stopped "+f.Name, func(ctx context.Context, be Backend) error { return be.Stop(ctx, f.ID) })
	default:
		return a.act("Started "+f.Name, func(ctx context.Context, be Backend) error { return be.Start(ctx, f.ID) })
	}
}

// testConn tries the connection the way the MCP server would, and reports what it found.
func (a *App) testConn(c external.Conn) tea.Cmd {
	be, conns := a.be, a.conns
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		res, err := mcpserver.New(be, conns, c.Name, "test").Probe(ctx)
		if err != nil {
			return connTestMsg{name: c.Name, err: err}
		}
		return connTestMsg{name: c.Name, summary: res}
	}
}

func (a *App) onConnFormDone(m connFormDoneMsg) tea.Cmd {
	a.cf = nil
	a.mode = modeBrowse
	if !m.saved {
		return nil
	}
	// Renaming a connection writes a new file; drop the old one.
	if !m.isNew && a.pendingConn.Name != "" && a.pendingConn.Name != m.conn.Name {
		_ = a.conns.Delete(a.pendingConn.Name)
	}
	if err := a.conns.Save(m.conn); err != nil {
		return a.setToast("Could not save the connection: "+err.Error(), true)
	}
	a.reloadConns()
	a.ext.selectName(m.conn.Name)
	a.tab = tabExternal
	return a.setToast("Saved “"+m.conn.Name+"”. Press m to copy the command that lets Claude read it.", false)
}

// ---------------------------------------------------------------- mcp command

// askWhichAITool opens the small dialog that picks the AI tool to write the
// command for. The name is remembered until the dialog is answered.
func (a *App) askWhichAITool(name string) tea.Cmd {
	a.pendingMCPName = name
	opts := make([]confirmOpt, 0, len(aiTools))
	for _, t := range aiTools {
		opts = append(opts, confirmOpt{key: t.key, label: t.name, value: t.key})
	}
	a.conf = &confirm{
		id:    "mcp-tool",
		title: "Copy the command for which tool?",
		body: "MiniDock copies one line you paste in a terminal. It gives that tool " +
			"read-only access to " + name + ".",
		opts: opts,
	}
	a.mode = modeConfirm
	return nil
}

// copyMCPCommand writes the command for the chosen tool to the clipboard.
func (a *App) copyMCPCommand(key string) tea.Cmd {
	t, ok := aiToolByKey(key)
	if !ok {
		return nil
	}
	name := a.pendingMCPName
	cmd := mcpCommandForTool(t, name)
	if err := copyToClipboard(cmd); err != nil {
		return a.setToast("Could not copy ("+err.Error()+"). Run: "+cmd, true)
	}
	return a.setToast(t.name+" command copied. Paste it in your terminal to let it read "+name+" (read-only).", false)
}

// ---------------------------------------------------------------- view

func (a *App) bannerShown() bool { return a.statusKnown && !a.status.Connected }

// bodyHeight is the space left for the screen between header and status bar.
func (a *App) bodyHeight() int {
	h := a.height - 2 - 2
	if a.bannerShown() {
		h--
	}
	return maxInt(h, 5)
}

func (a *App) resizeChildren() {
	bh := a.bodyHeight()
	if a.wiz != nil {
		a.wiz.resize(a.width, bh)
	}
	if a.det != nil {
		a.det.resize(a.width, bh)
	}
	if a.ed != nil {
		a.ed.resize(a.width, bh)
	}
	if a.cf != nil {
		a.cf.resize(a.width, bh)
	}
}

func (a *App) hints() []hint {
	switch a.mode {
	case modeWizard:
		return a.wiz.Hints()
	case modeDetail:
		return hintsDetail
	case modeEditor:
		return hintsEditor
	case modeConfirm:
		return hintsConfirm
	case modePrompt:
		return hintsPrompt
	case modeConnForm:
		return hintsEditor
	}
	if a.tab == tabExternal {
		return hintsExternal
	}
	if g := a.activeGallery(); g != nil {
		if g.Filtering() {
			return hintsFilter
		}
		if a.tab == tabMine {
			return hintsMine
		}
		return hintsTemplates
	}
	return hintsServers
}

func (a *App) renderHeader() string {
	title := styleTitle.Render("◆ MiniDock")
	if a.mode == modeBrowse {
		tabs := make([]string, 0, tabCount)
		for i := tabID(0); i < tabCount; i++ {
			name := tabNames[i]
			if i == tabServers && len(a.instances) > 0 {
				running := 0
				for _, in := range a.instances {
					if in.Running() {
						running++
					}
				}
				name = fmt.Sprintf("%s (%d/%d)", name, running, len(a.instances))
			}
			if i == a.tab {
				tabs = append(tabs, styleTabOn.Render(name))
			} else {
				tabs = append(tabs, styleTabOff.Render(name))
			}
		}
		title += "   " + strings.Join(tabs, "")
	}
	return title + "\n" + styleDivider.Render(strings.Repeat("─", maxInt(a.width, 1)))
}

func (a *App) renderBody() string {
	w, h := a.width, a.bodyHeight()
	switch {
	case a.showHelp:
		return styleBold.Render(helpText)
	case a.mode == modeWizard && a.wiz != nil:
		return a.wiz.View()
	case a.mode == modeDetail && a.det != nil:
		return a.det.View()
	case a.mode == modeEditor && a.ed != nil:
		return a.ed.View()
	case a.mode == modeConnForm && a.cf != nil:
		return a.cf.View()
	case a.mode == modeConfirm && a.conf != nil:
		return "\n" + a.conf.View(w)
	case a.mode == modePrompt && a.prompt != nil:
		return "\n" + a.prompt.View()
	}

	switch a.tab {
	case tabServers:
		return a.servers.View(w, h)
	case tabExternal:
		extra := ""
		if len(a.extProblems) > 0 {
			extra = "\n" + styleWarn.Render(trunc("⚠ "+a.extProblems[0].Error(), w))
			h--
		}
		return a.ext.View(w, h) + extra
	default:
		g := a.activeGallery()
		extra := ""
		if a.tab == tabMine && len(a.problems) > 0 {
			extra = "\n" + styleWarn.Render(trunc("⚠ "+a.problems[0].Error(), w))
			h--
		}
		return g.View(w, h) + extra
	}
}

// View draws header, screen and status bar.
func (a *App) View() string {
	if a.width == 0 {
		return ""
	}
	parts := []string{a.renderHeader()}
	if a.bannerShown() {
		parts = append(parts, renderBanner(a.width, a.status))
	}
	body := lipgloss.NewStyle().Width(a.width).Height(a.bodyHeight()).MaxHeight(a.bodyHeight()).MaxWidth(a.width).Render(a.renderBody())
	parts = append(parts, body, renderStatusBar(a.width, a.status, a.statusKnown, a.toast, a.toastErr, a.hints()))
	return strings.Join(parts, "\n")
}

package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"minidock/internal/docker"
	"minidock/internal/external"
	"minidock/internal/template"
)

// ---------------------------------------------------------------- fake backend

type fakeBackend struct {
	mu        sync.Mutex
	status    docker.Status
	instances []docker.Instance
	foreign   []docker.Foreign
	used      map[int]bool
	created   []template.RunSpec
	createErr error
	calls     []string
	logLines  []string
	execOut   docker.ExecResult
	execErr   error
}

func (f *fakeBackend) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeBackend) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

func (f *fakeBackend) Ping(context.Context) docker.Status { return f.status }
func (f *fakeBackend) List(context.Context) ([]docker.Instance, error) {
	return f.instances, nil
}
func (f *fakeBackend) UsedPorts(context.Context) (map[int]bool, error) { return f.used, nil }
func (f *fakeBackend) ListForeign(context.Context) ([]docker.Foreign, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.foreign, nil
}
func (f *fakeBackend) FindContainer(_ context.Context, nameOrID string) (docker.Foreign, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.foreign {
		if c.Name == nameOrID || c.ID == nameOrID {
			return c, nil
		}
	}
	return docker.Foreign{}, errors.New("no Docker container named " + nameOrID + " is on this machine")
}
func (f *fakeBackend) Exec(_ context.Context, id string, _, _ []string, _ int) (docker.ExecResult, error) {
	f.record("exec " + id)
	return f.execOut, f.execErr
}
func (f *fakeBackend) Create(_ context.Context, spec template.RunSpec, on func(docker.Progress)) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	on(docker.Progress{Status: "Pulling…", Percent: 0.5})
	f.mu.Lock()
	f.created = append(f.created, spec)
	f.mu.Unlock()
	return "new-id", nil
}
func (f *fakeBackend) Start(_ context.Context, id string) error { f.record("start " + id); return nil }
func (f *fakeBackend) Stop(_ context.Context, id string) error  { f.record("stop " + id); return nil }
func (f *fakeBackend) Restart(_ context.Context, id string) error {
	f.record("restart " + id)
	return nil
}
func (f *fakeBackend) Remove(_ context.Context, inst docker.Instance, data bool) error {
	if data {
		f.record("remove+data " + inst.ID)
	} else {
		f.record("remove " + inst.ID)
	}
	return nil
}
func (f *fakeBackend) Logs(context.Context, string, int) (*docker.LogStream, error) {
	ch := make(chan string, len(f.logLines))
	for _, l := range f.logLines {
		ch <- l
	}
	close(ch)
	return &docker.LogStream{Lines: ch}, nil
}

// ---------------------------------------------------------------- harness

// harness drives the App like Bubble Tea would: it feeds messages in and
// runs the commands that come back, feeding their results in again.
type harness struct {
	t   *testing.T
	app *App
	be  *fakeBackend
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	be := &fakeBackend{
		status: docker.Status{Connected: true, Version: "27.0"},
		used:   map[int]bool{},
		instances: []docker.Instance{
			{ID: "id-pg", Name: "pg-main", TemplateID: "postgres", Version: "17", Image: "postgres:17", State: "running",
				StatusText: "Up 2 minutes", HostPort: 5432, ContainerPort: 5432, Volume: "minidock-pg-main-data",
				Connection: "postgresql://postgres:postgres@localhost:5432/app"},
			{ID: "id-redis", Name: "cache", TemplateID: "redis", Version: "7", Image: "redis:7", State: "exited",
				StatusText: "Exited (0) 1 hour ago", HostPort: 6379, ContainerPort: 6379},
		},
		logLines: []string{"database system is ready", "listening on port 5432"},
	}
	app := New(be, template.NewStore(t.TempDir()), external.NewStore(t.TempDir()))
	h := &harness{t: t, app: app, be: be}
	h.send(tea.WindowSizeMsg{Width: 110, Height: 32})
	return h
}

var cmdType = reflect.TypeOf((tea.Cmd)(nil))

// resolve runs a command and any commands nested in batches or sequences.
// Commands that block longer than the deadline (cursor blinks, timers) are dropped.
func resolve(cmd tea.Cmd, deadline time.Time) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(time.Until(deadline)):
		return nil
	}
	if msg == nil {
		return nil
	}
	v := reflect.ValueOf(msg)
	if v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		results := make([][]tea.Msg, v.Len())
		var wg sync.WaitGroup
		for i := 0; i < v.Len(); i++ {
			sub, _ := v.Index(i).Interface().(tea.Cmd)
			wg.Add(1)
			go func(i int, sub tea.Cmd) {
				defer wg.Done()
				results[i] = resolve(sub, deadline)
			}(i, sub)
		}
		wg.Wait()
		var out []tea.Msg
		for _, r := range results {
			out = append(out, r...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	queue := []tea.Msg{msg}
	for steps := 0; len(queue) > 0 && steps < 200; steps++ {
		m := queue[0]
		queue = queue[1:]
		_, cmd := h.app.Update(m)
		queue = append(queue, resolve(cmd, time.Now().Add(120*time.Millisecond))...)
	}
}

func (h *harness) key(s string) {
	h.t.Helper()
	var km tea.KeyMsg
	switch s {
	case "enter":
		km = tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		km = tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		km = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		km = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		km = tea.KeyMsg{Type: tea.KeyUp}
	default:
		km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	h.send(km)
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (h *harness) view() string { return h.app.View() }

// gotoTab presses tab until the wanted tab is open, so a test does not have to
// know how many tabs there are or in which order they sit.
func (h *harness) gotoTab(want tabID) {
	h.t.Helper()
	for i := 0; i < int(tabCount); i++ {
		if h.app.tab == want {
			return
		}
		h.key("tab")
	}
	h.t.Fatalf("could not reach tab %d", want)
}

func (h *harness) mustSee(parts ...string) {
	h.t.Helper()
	v := h.view()
	for _, p := range parts {
		if !strings.Contains(v, p) {
			h.t.Errorf("screen should contain %q, got:\n%s", p, v)
		}
	}
}

func (h *harness) mustNotSee(parts ...string) {
	h.t.Helper()
	v := h.view()
	for _, p := range parts {
		if strings.Contains(v, p) {
			h.t.Errorf("screen should NOT contain %q, got:\n%s", p, v)
		}
	}
}

// connect feeds a status, the server list and the containers MiniDock did not
// create, like the first refresh would.
func (h *harness) connect() {
	h.send(statusMsg{status: h.be.status})
	h.send(instancesMsg{list: h.be.instances, used: h.be.used})
	h.send(foreignMsg{list: h.be.foreign})
}

// ---------------------------------------------------------------- phase 1

func TestStartsOnTemplatesTabAndTabsWrapAround(t *testing.T) {
	h := newHarness(t)
	h.mustSee("MiniDock", "Templates", "Servers", "External", "My Templates", "Checking Docker")

	h.key("tab")
	if h.app.tab != tabServers {
		t.Errorf("tab = %d, want servers", h.app.tab)
	}
	for i := tabServers; i < tabCount; i++ {
		h.key("tab")
	}
	if h.app.tab != tabTemplates {
		t.Errorf("tabs should wrap around, got %d", h.app.tab)
	}
	h.send(tea.KeyMsg{Type: tea.KeyShiftTab})
	if h.app.tab != tabCount-1 {
		t.Errorf("shift+tab should go back, got %d", h.app.tab)
	}
}

func TestDockerStatusShownInStatusBar(t *testing.T) {
	h := newHarness(t)
	h.send(statusMsg{status: docker.Status{Connected: true, Version: "27.0"}})
	h.mustSee("Docker connected 27.0")
	h.mustNotSee("Docker not running")

	h.send(statusMsg{status: docker.Status{Hint: "Docker is not running. Start Docker Desktop (or the Docker service), then press r."}})
	h.mustSee("Docker not running", "Start Docker Desktop")
}

func TestHelpOpensAndAnyKeyCloses(t *testing.T) {
	h := newHarness(t)
	h.key("?")
	h.mustSee("Move around")
	h.key("x")
	h.mustNotSee("Move around")
}

func TestQuitKey(t *testing.T) {
	h := newHarness(t)
	_, cmd := h.app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("q should quit")
	}
}

// ---------------------------------------------------------------- phase 2

func TestGalleryShowsBuiltinTemplatesWithPreview(t *testing.T) {
	h := newHarness(t)
	h.mustSee("PostgreSQL", "Redis", "MySQL", "MongoDB", "Image", "postgres", "17, 16, 15, 14")

	h.key("down")
	h.mustSee("Fast in-memory key-value store")
}

func TestGallerySearch(t *testing.T) {
	h := newHarness(t)
	h.key("/")
	h.typeText("redis")
	h.mustSee("Redis")
	h.mustNotSee("PostgreSQL", "MongoDB")

	h.key("esc")
	h.mustSee("PostgreSQL", "MongoDB")

	h.key("/")
	h.typeText("zzz")
	h.mustSee("No template matches")
}

func TestSmallTerminalStillRenders(t *testing.T) {
	h := newHarness(t)
	h.send(tea.WindowSizeMsg{Width: 50, Height: 14})
	h.mustSee("MiniDock", "PostgreSQL")
	for i, line := range strings.Split(h.view(), "\n") {
		if w := len([]rune(line)); w > 200 { // sanity: no runaway lines
			t.Errorf("line %d too long (%d)", i, w)
		}
	}
}

// ---------------------------------------------------------------- phase 3

func TestWizardCreatesPostgresServer(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.mustSee("Docker connected")

	h.key("enter") // use PostgreSQL
	if h.app.mode != modeWizard {
		t.Fatalf("mode = %d, want wizard", h.app.mode)
	}
	h.mustSee("Server name", "postgres")
	if got := *h.app.wiz.vals["name"]; got != "postgres" {
		t.Errorf("default name = %q", got)
	}

	// name, version, port, user, password, database
	for i := 0; i < 6; i++ {
		h.key("enter")
	}
	if h.app.wiz.stage != stageReview {
		t.Fatalf("stage = %d, want review; screen:\n%s", h.app.wiz.stage, h.view())
	}
	h.mustSee("Review", "docker run -d", "--name postgres", "POSTGRES_PASSWORD=****", "postgres:17")
	h.mustNotSee("POSTGRES_PASSWORD=postgres")

	h.key("enter") // create & start
	if h.app.wiz.stage != stageDone {
		t.Fatalf("stage = %d, want done; screen:\n%s", h.app.wiz.stage, h.view())
	}
	h.mustSee("is running on localhost", "postgresql://postgres:postgres@localhost:")
	if len(h.be.created) != 1 {
		t.Fatalf("created %d servers", len(h.be.created))
	}
	spec := h.be.created[0]
	if spec.Image != "postgres:17" || spec.Name != "postgres" || spec.Volume != "minidock-postgres-data" {
		t.Errorf("bad spec: %+v", spec)
	}

	h.key("enter") // go to servers
	if h.app.mode != modeBrowse || h.app.tab != tabServers {
		t.Errorf("should land on the Servers tab, mode=%d tab=%d", h.app.mode, h.app.tab)
	}
	h.mustSee("Server created")
}

func TestWizardDefaultsAvoidTakenNamesAndPorts(t *testing.T) {
	h := newHarness(t)
	h.be.instances = []docker.Instance{{ID: "x", Name: "postgres", State: "running"}}
	h.be.used = map[int]bool{5432: true, 5433: true}
	h.connect()

	h.key("enter")
	if got := *h.app.wiz.vals["name"]; got != "postgres-2" {
		t.Errorf("name = %q, want postgres-2", got)
	}
	if got := *h.app.wiz.vals["port"]; got == "5432" || got == "5433" {
		t.Errorf("port = %s should skip used ports", got)
	}
}

func TestWizardRejectsInvalidAnswers(t *testing.T) {
	h := newHarness(t)
	h.be.instances = []docker.Instance{{ID: "x", Name: "taken", State: "running"}}
	h.be.used = map[int]bool{7777: true}
	h.connect()
	h.key("enter")

	wz := h.app.wiz
	if err := wz.validatePort("7777"); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Errorf("used port: %v", err)
	}
	if err := wz.validatePort("abc"); err == nil {
		t.Error("non-numeric port should fail")
	}
	if err := template.ValidateInstanceName("taken", wz.taken); err == nil {
		t.Error("taken name should fail")
	}

	// Try to submit an empty name: the form must not move on.
	*wz.vals["name"] = ""
	h.send(tea.KeyMsg{Type: tea.KeyCtrlA}) // no-op key, just to rebuild nothing
	h.key("enter")
	if wz.stage != stageForm {
		t.Error("empty name must keep the wizard on the form")
	}
}

func TestWizardShowsFriendlyCreateError(t *testing.T) {
	h := newHarness(t)
	h.be.createErr = errors.New("Bind for 127.0.0.1:5432 failed: port is already allocated")
	h.connect()
	h.key("enter")
	for i := 0; i < 6; i++ {
		h.key("enter")
	}
	h.key("enter")
	if h.app.wiz.stage != stageFailed {
		t.Fatalf("stage = %d, want failed", h.app.wiz.stage)
	}
	h.mustSee("Could not create the server", "port is already in use")

	h.key("esc")
	if h.app.wiz.stage != stageReview {
		t.Error("esc should return to the review screen")
	}
	h.key("esc")
	if h.app.wiz.stage != stageForm {
		t.Error("esc on review should return to the form")
	}
	h.key("esc")
	if h.app.mode != modeBrowse {
		t.Error("esc on the form should close the wizard")
	}
}

func TestWizardBlockedWhenDockerIsDown(t *testing.T) {
	h := newHarness(t)
	h.send(statusMsg{status: docker.Status{Hint: "Docker is not running."}})
	h.key("enter")
	if h.app.mode != modeBrowse {
		t.Error("wizard should not open while Docker is down")
	}
	h.mustSee("Docker is not running")
}

// ---------------------------------------------------------------- phase 4

func TestServersListAndStartStop(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.key("tab")
	h.mustSee("pg-main", "cache", "Up 2 minutes", ":5432", ":6379", "Servers (1/2)")

	h.key("s") // pg-main is running → stop
	if !h.be.called("stop id-pg") {
		t.Errorf("expected stop, calls = %v", h.be.calls)
	}
	h.mustSee("Stopped pg-main")

	h.key("down")
	h.key("s") // cache is stopped → start
	if !h.be.called("start id-redis") {
		t.Errorf("expected start, calls = %v", h.be.calls)
	}

	h.key("R")
	if !h.be.called("restart id-redis") {
		t.Errorf("expected restart, calls = %v", h.be.calls)
	}
}

func TestServerDeleteAsksAboutData(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.key("tab")

	h.key("d")
	if h.app.mode != modeConfirm {
		t.Fatalf("mode = %d, want confirm", h.app.mode)
	}
	h.mustSee("Delete server", "keep data", "AND data")

	h.key("n") // cancel
	if h.app.mode != modeBrowse || len(h.be.calls) != 0 {
		t.Errorf("cancel should do nothing, calls = %v", h.be.calls)
	}

	h.key("d")
	h.key("d") // delete container and data
	if !h.be.called("remove+data id-pg") {
		t.Errorf("expected remove+data, calls = %v", h.be.calls)
	}

	h.key("d")
	h.key("y") // container only
	if !h.be.called("remove id-pg") {
		t.Errorf("expected remove, calls = %v", h.be.calls)
	}
}

func TestServerWithoutVolumeOffersOnlyContainerDelete(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.key("tab")
	h.key("down") // "cache" has no volume
	h.key("d")
	h.mustSee("Delete container, keep data")
	h.mustNotSee("AND data")
}

func TestServerDetailShowsLogsAndInfo(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.key("tab")
	h.key("enter")
	if h.app.mode != modeDetail {
		t.Fatalf("mode = %d, want detail", h.app.mode)
	}
	h.mustSee("pg-main", "postgres:17", "minidock-pg-main-data",
		"postgresql://postgres:postgres@localhost:5432/app", "database system is ready", "listening on port 5432")

	h.key("s")
	if !h.be.called("stop id-pg") {
		t.Errorf("detail should stop the server, calls = %v", h.be.calls)
	}

	h.key("esc")
	if h.app.mode != modeBrowse || h.app.det != nil {
		t.Error("esc should close the detail view")
	}
}

func TestDetailClosesWhenServerDisappears(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.key("tab")
	h.key("enter")
	h.send(instancesMsg{list: nil})
	if h.app.mode != modeBrowse {
		t.Error("detail should close when the server is gone")
	}
	h.mustSee("no longer exists")
}

func TestServersEmptyAndErrorStates(t *testing.T) {
	h := newHarness(t)
	h.key("tab")
	h.mustSee("Loading servers")

	h.send(instancesMsg{})
	h.mustSee("No servers yet")

	h.send(instancesMsg{err: errors.New("Cannot connect to the Docker daemon. Is the docker daemon running?")})
	h.mustSee("Could not load servers", "Docker is not running")
}

// ---------------------------------------------------------------- phase 5

func TestDuplicateEditAndDeleteCustomTemplate(t *testing.T) {
	h := newHarness(t)

	// Duplicate PostgreSQL → editor opens prefilled.
	h.key("u")
	if h.app.mode != modeEditor {
		t.Fatalf("mode = %d, want editor", h.app.mode)
	}
	h.mustSee("New template", "PostgreSQL (copy)")
	ed := h.app.ed
	if ed.f.id != "postgres-copy" || ed.f.image != "postgres" {
		t.Errorf("prefill wrong: %+v", ed.f)
	}

	// Change a few things and save through the same path the form uses.
	ed.f.id, ed.f.name, ed.f.image = "my-pg", "My Postgres", "postgres"
	tpl, err := ed.build()
	if err != nil {
		t.Fatal(err)
	}
	h.send(editorDoneMsg{tpl: tpl, isNew: true, saved: true})
	if h.app.tab != tabMine {
		t.Error("saving should switch to My Templates")
	}
	h.mustSee("My Postgres", "custom", "Saved template")
	if _, ok := h.app.store.Get("my-pg"); !ok {
		t.Fatal("template was not saved to disk")
	}

	// The custom template appears in the Templates tab too, and can be used.
	h.send(tea.KeyMsg{Type: tea.KeyShiftTab}) // → Servers
	h.send(tea.KeyMsg{Type: tea.KeyShiftTab}) // → Templates
	h.mustSee("My Postgres")

	// Edit it.
	h.key("tab")
	h.key("tab")
	h.key("e")
	if h.app.mode != modeEditor || h.app.ed.isNew {
		t.Fatalf("edit should open the editor for an existing template")
	}
	h.mustSee("Edit template", "Template id: my-pg")
	h.key("esc")
	if h.app.mode != modeBrowse {
		t.Error("esc should cancel the editor")
	}

	// Delete it.
	h.key("d")
	h.mustSee("Delete template")
	h.key("y")
	if _, ok := h.app.store.Get("my-pg"); ok {
		t.Error("template should be deleted")
	}
	h.mustSee("You have no custom templates")
}

func TestBuiltinTemplatesCannotBeEditedOrDeleted(t *testing.T) {
	h := newHarness(t)
	h.key("e")
	h.mustSee("can't be edited")
	if h.app.mode != modeBrowse {
		t.Error("editor must not open for built-ins")
	}
	h.key("d")
	h.mustSee("can't be deleted")
}

func TestExportAndImportTemplate(t *testing.T) {
	h := newHarness(t)
	dst := t.TempDir() + "/pg.json"

	h.key("x")
	if h.app.mode != modePrompt {
		t.Fatalf("mode = %d, want prompt", h.app.mode)
	}
	h.mustSee("Export", "./postgres.json")
	h.app.prompt.input.SetValue(dst)
	h.key("enter")
	h.mustSee("Exported to")

	// A broken path is reported, not a crash.
	h.key("i")
	h.mustSee("Import a template")
	h.app.prompt.input.SetValue(t.TempDir() + "/missing.json")
	h.key("enter")
	h.mustSee("Import failed")

	// Importing the exported built-in fails because the id already exists.
	h.key("i")
	h.app.prompt.input.SetValue(dst)
	h.key("enter")
	h.mustSee("already exists")

	// Cancel works.
	h.key("i")
	h.key("esc")
	if h.app.mode != modeBrowse {
		t.Error("esc should close the prompt")
	}
}

func TestEditorBuildValidation(t *testing.T) {
	h := newHarness(t)
	h.key("n")
	ed := h.app.ed
	ed.f = editorFields{id: "web", name: "Web", image: "nginx", cport: "80", env: "A=1", fields: "who | Who | text | me | yes", conn: "http://{{host}}:{{port}}/{{who}}"}
	tpl, err := ed.build()
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Env["A"] != "1" || tpl.Fields[0].Key != "who" || !tpl.Fields[0].Required {
		t.Errorf("bad template: %+v", tpl)
	}

	ed.f.env = "not-valid"
	if _, err := ed.build(); err == nil {
		t.Error("bad env should fail")
	}
	ed.f.env = ""
	ed.f.conn = "{{nope}}"
	if _, err := ed.build(); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown placeholder should fail: %v", err)
	}
	ed.f.conn = ""
	ed.f.cport = "x"
	if _, err := ed.build(); err == nil {
		t.Error("bad port should fail")
	}
}

func TestCustomTemplateCanBeUsedInWizard(t *testing.T) {
	h := newHarness(t)
	custom := template.Template{
		ID: "my-nginx", Name: "My Nginx", Icon: "🌐", Image: "nginx", ContainerPort: 80, DefaultHostPort: 8080,
		Versions: []string{"alpine"}, ConnectionString: "http://{{host}}:{{port}}",
	}
	if err := h.app.store.Save(custom); err != nil {
		t.Fatal(err)
	}
	h.key("r") // reload
	h.connect()
	h.gotoTab(tabMine)
	h.key("enter")
	if h.app.mode != modeWizard {
		t.Fatalf("mode = %d, want wizard", h.app.mode)
	}
	// name, version, port
	for i := 0; i < 3; i++ {
		h.key("enter")
	}
	h.key("enter")
	if len(h.be.created) != 1 || h.be.created[0].Image != "nginx:alpine" {
		t.Fatalf("created = %+v", h.be.created)
	}
}

// ---------------------------------------------------------------- phase 6

func TestSizesNeverPanic(t *testing.T) {
	h := newHarness(t)
	h.connect()
	for _, size := range [][2]int{{20, 6}, {40, 10}, {80, 24}, {200, 60}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for tab := 0; tab < 3; tab++ {
			_ = h.view()
			h.key("tab")
		}
		h.key("enter")
		_ = h.view()
		h.key("esc")
	}
}

func TestToastClearsOnlyItsOwnMessage(t *testing.T) {
	h := newHarness(t)
	h.app.setToast("first", false)
	h.app.setToast("second", false)
	h.send(toastClearMsg{seq: 1})
	h.mustSee("second")
	h.send(toastClearMsg{seq: 2})
	h.mustNotSee("second")
}

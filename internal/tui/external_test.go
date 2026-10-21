package tui

import (
	"strings"
	"testing"

	"minidock/internal/docker"
	"minidock/internal/external"
)

// withExternal opens the External tab with one saved connection and one
// MySQL container MiniDock did not create, capturing what would be copied.
func withExternal(t *testing.T) (*harness, *[]string) {
	t.Helper()
	var copied []string
	old := copyToClipboard
	copyToClipboard = func(s string) error { copied = append(copied, s); return nil }
	t.Cleanup(func() { copyToClipboard = old })

	h := newHarness(t)
	h.be.foreign = []docker.Foreign{{
		ID: "ctr-shop", Name: "shop-db-1", Image: "mysql:8.4", State: "running",
		StatusText: "Up 5 minutes", Client: "mysql", Compose: "shop",
	}}
	if err := h.app.conns.Save(external.Conn{
		Name: "shop", Container: "shop-db-1", Client: "mysql",
		User: "reader", Password: "s3cret", Database: "shop", Note: "staging copy",
	}); err != nil {
		t.Fatal(err)
	}
	h.app.reloadConns()
	h.connect()
	h.gotoTab(tabExternal)
	return h, &copied
}

func TestExternalTabListsSavedConnections(t *testing.T) {
	h, _ := withExternal(t)
	h.mustSee("shop", "reader@shop-db-1", "staging copy")
	// The password is never on screen.
	h.mustNotSee("s3cret")
}

func TestExternalTabIsEmptyAtFirstAndExplainsItself(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.gotoTab(tabExternal)
	h.mustSee("No connections to existing databases yet", "read-only")
}

func TestExternalMCPKeyCopiesCommandForTheConnection(t *testing.T) {
	h, copied := withExternal(t)
	h.key("m")
	if len(*copied) != 1 {
		t.Fatalf("expected one copy, got %v", *copied)
	}
	cmd := (*copied)[0]
	for _, want := range []string{"claude mcp add minidock-shop -- ", " mcp --server shop"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q should contain %q", cmd, want)
		}
	}
	if strings.Contains(cmd, "s3cret") {
		t.Error("the command must not contain the password")
	}
	h.mustSee("MCP command copied")
}

func TestExternalTestKeyReportsWhatItFound(t *testing.T) {
	h, _ := withExternal(t)
	h.be.execOut = docker.ExecResult{Stdout: []byte(
		`<?xml version="1.0"?><resultset><row><field name="Database">shop</field></row></resultset>`)}
	h.key("t")
	h.mustSee("shop", "works")
}

func TestExternalTestKeyReportsAFailure(t *testing.T) {
	h, _ := withExternal(t)
	h.be.foreign = nil // the container went away
	h.key("t")
	h.mustSee("Could not read shop")
}

func TestExternalDeleteAsksFirstAndKeepsTheContainer(t *testing.T) {
	h, _ := withExternal(t)
	h.key("d")
	if h.app.mode != modeConfirm {
		t.Fatalf("mode = %d, want confirm", h.app.mode)
	}
	// It must be clear that the database itself is not touched.
	h.mustSee("shop-db-1", "left untouched")

	h.key("y")
	if got, _ := h.app.conns.List(); len(got) != 0 {
		t.Errorf("the connection should be gone, got %+v", got)
	}
	if h.be.called("stop ctr-shop") || h.be.called("exec ctr-shop") {
		t.Error("deleting a connection must not touch the container")
	}
	h.mustSee("Removed connection")
}

func TestExternalDeleteCanBeCancelled(t *testing.T) {
	h, _ := withExternal(t)
	h.key("d")
	h.key("esc")
	if got, _ := h.app.conns.List(); len(got) != 1 {
		t.Errorf("cancelling should keep the connection, got %+v", got)
	}
}

func TestExternalNewOpensTheFormWithTheScannedContainers(t *testing.T) {
	h, _ := withExternal(t)
	h.key("n")
	if h.app.mode != modeConnForm {
		t.Fatalf("mode = %d, want the connection form", h.app.mode)
	}
	// The container MiniDock found is offered, with its compose project to tell it apart.
	h.mustSee("shop-db-1", "shop")
}

func TestExternalNewSaysSoWhenThereIsNothingToConnectTo(t *testing.T) {
	h := newHarness(t)
	h.connect()
	h.gotoTab(tabExternal)
	h.key("n")
	h.mustSee("No other containers found", "docker compose up")
}

func TestExternalEditPrefillsTheForm(t *testing.T) {
	h, _ := withExternal(t)
	h.key("e")
	if h.app.mode != modeConnForm {
		t.Fatalf("mode = %d, want the connection form", h.app.mode)
	}
	if h.app.cf.f.user != "reader" || h.app.cf.f.name != "shop" || h.app.cf.f.password != "s3cret" {
		t.Errorf("form not prefilled: %+v", h.app.cf.f)
	}
}

// Renaming must not leave the old connection behind under its old name.
func TestRenamingAConnectionReplacesIt(t *testing.T) {
	h, _ := withExternal(t)
	h.key("e")
	renamed := external.Conn{
		Name: "shop-staging", Container: "shop-db-1", Client: "mysql", User: "reader", Password: "s3cret",
	}
	h.send(connFormDoneMsg{conn: renamed, saved: true})

	all, _ := h.app.conns.List()
	if len(all) != 1 || all[0].Name != "shop-staging" {
		t.Fatalf("expected only the renamed connection, got %+v", all)
	}
}

func TestSavedConnectionSurvivesARestart(t *testing.T) {
	h, _ := withExternal(t)
	// A fresh store on the same folder is what the next run sees.
	again := external.NewStore(h.app.conns.Dir())
	got, ok := again.Get("shop")
	if !ok || got.User != "reader" || got.Password != "s3cret" {
		t.Fatalf("connection did not survive: %+v (found %v)", got, ok)
	}
}

func TestHelpMentionsTheExternalTab(t *testing.T) {
	h := newHarness(t)
	h.key("?")
	h.mustSee("databases MiniDock did not create", "read-only")
}

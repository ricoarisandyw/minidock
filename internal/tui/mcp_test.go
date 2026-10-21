package tui

import (
	"strings"
	"testing"

	"minidock/internal/docker"
)

// withMySQL puts a MySQL server first in the list and captures what would be copied.
func withMySQL(t *testing.T) (*harness, *[]string) {
	t.Helper()
	var copied []string
	old := copyToClipboard
	copyToClipboard = func(s string) error { copied = append(copied, s); return nil }
	t.Cleanup(func() { copyToClipboard = old })

	h := newHarness(t)
	h.be.instances = append([]docker.Instance{{
		ID: "id-my", Name: "shop-db", TemplateID: "mysql", Version: "8.4", Image: "mysql:8.4", State: "running",
		StatusText: "Up 1 minute", HostPort: 3306, ContainerPort: 3306,
		Connection: "mysql://root:secret@localhost:3306/shop",
	}}, h.be.instances...)
	h.connect()
	h.key("tab")
	return h, &copied
}

func TestMCPKeyCopiesCommandForMySQL(t *testing.T) {
	h, copied := withMySQL(t)
	h.mustSee("shop-db", "mcp") // the hint bar lists the key

	h.key("m")
	if len(*copied) != 1 {
		t.Fatalf("expected one copy, got %v", *copied)
	}
	cmd := (*copied)[0]
	for _, want := range []string{"claude mcp add minidock-shop-db -- ", " mcp --server shop-db"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q should contain %q", cmd, want)
		}
	}
	if strings.Contains(cmd, "secret") {
		t.Error("the command must not contain the password")
	}
	h.mustSee("MCP command copied")
}

func TestMCPKeyRefusesOtherServers(t *testing.T) {
	h, copied := withMySQL(t)
	h.key("down") // pg-main
	h.key("m")
	if len(*copied) != 0 {
		t.Errorf("nothing should be copied for PostgreSQL, got %v", *copied)
	}
	h.mustSee("only available for MySQL and MariaDB")
}

func TestMCPKeyWorksInDetailScreen(t *testing.T) {
	h, copied := withMySQL(t)
	h.key("enter")
	if h.app.mode != modeDetail {
		t.Fatalf("mode = %d, want detail", h.app.mode)
	}
	h.key("m")
	if len(*copied) != 1 || !strings.Contains((*copied)[0], "--server shop-db") {
		t.Errorf("unexpected copy: %v", *copied)
	}
}

func TestHelpMentionsMCPKey(t *testing.T) {
	h := newHarness(t)
	h.key("?")
	h.mustSee("copy the command that lets Claude read")
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"minidock-db": "minidock-db",
		"/usr/bin/x":  "/usr/bin/x",
		"My Apps/x":   "'My Apps/x'",
		"it's":        `'it'\''s'`,
		"":            "''",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

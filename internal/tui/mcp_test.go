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
	h.key("c") // Claude Code
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
	h.mustSee("Claude Code command copied")
}

// PostgreSQL is a database MiniDock can read, so m works there too.
func TestMCPKeyWorksForPostgres(t *testing.T) {
	h, copied := withMySQL(t)
	h.key("down") // pg-main
	h.key("m")
	h.key("c")
	if len(*copied) != 1 || !strings.Contains((*copied)[0], "--server pg-main") {
		t.Errorf("Postgres should get an MCP command, got %v", *copied)
	}
}

// Redis is not a database this MCP can read, and saying so is the point.
func TestMCPKeyRefusesNonDatabases(t *testing.T) {
	h, copied := withMySQL(t)
	h.key("down")
	h.key("down") // cache (redis)
	h.key("m")
	if len(*copied) != 0 {
		t.Errorf("nothing should be copied for Redis, got %v", *copied)
	}
	h.mustSee("only available for MySQL, MariaDB and PostgreSQL")
}

func TestMCPKeyWorksInDetailScreen(t *testing.T) {
	h, copied := withMySQL(t)
	h.key("enter")
	if h.app.mode != modeDetail {
		t.Fatalf("mode = %d, want detail", h.app.mode)
	}
	h.key("m")
	h.key("c")
	if len(*copied) != 1 || !strings.Contains((*copied)[0], "--server shop-db") {
		t.Errorf("unexpected copy: %v", *copied)
	}
}

func TestHelpMentionsMCPKey(t *testing.T) {
	h := newHarness(t)
	h.key("?")
	h.mustSee("copy the command that lets an AI tool read", "Claude Code, Codex or Gemini CLI")
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

// ---------------------------------------------------------------- choosing the AI tool

func TestMCPKeyAsksWhichToolFirst(t *testing.T) {
	h, copied := withMySQL(t)
	h.key("m")
	if h.app.mode != modeConfirm {
		t.Fatalf("mode = %d, want the tool dialog", h.app.mode)
	}
	h.mustSee("which tool", "Claude Code", "Codex", "Gemini CLI", "shop-db")
	if len(*copied) != 0 {
		t.Errorf("nothing should be copied before a tool is chosen, got %v", *copied)
	}
}

func TestEachToolGetsItsOwnCommand(t *testing.T) {
	cases := map[string]string{
		"c": "claude mcp add minidock-shop-db -- ",
		"x": "codex mcp add minidock-shop-db -- ",
		"g": "gemini mcp add minidock-shop-db -- ",
	}
	for key, want := range cases {
		h, copied := withMySQL(t)
		h.key("m")
		h.key(key)
		if len(*copied) != 1 {
			t.Fatalf("%s: expected one copy, got %v", key, *copied)
		}
		got := (*copied)[0]
		if !strings.HasPrefix(got, want) {
			t.Errorf("%s: command %q should start with %q", key, got, want)
		}
		if !strings.Contains(got, "mcp --server shop-db") {
			t.Errorf("%s: command %q should pass the server name", key, got)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("%s: the command must not contain the password: %q", key, got)
		}
	}
}

func TestToolDialogCanBeCancelled(t *testing.T) {
	h, copied := withMySQL(t)
	h.key("m")
	h.key("esc")
	if len(*copied) != 0 {
		t.Errorf("cancelling should copy nothing, got %v", *copied)
	}
	if h.app.mode != modeBrowse {
		t.Errorf("mode = %d, want browse after cancelling", h.app.mode)
	}
}

// The dialog is offered on the External tab too, and names the connection.
func TestExternalMCPKeyAsksWhichToolFirst(t *testing.T) {
	h, copied := withExternal(t)
	h.key("m")
	if h.app.mode != modeConfirm {
		t.Fatalf("mode = %d, want the tool dialog", h.app.mode)
	}
	h.mustSee("Codex", "Gemini CLI", "shop")

	h.key("x")
	if len(*copied) != 1 || !strings.HasPrefix((*copied)[0], "codex mcp add minidock-shop -- ") {
		t.Errorf("unexpected copy: %v", *copied)
	}
	h.mustSee("Codex command copied")
}

// Every tool must be reachable by its own key, and the keys must stay distinct.
func TestAIToolKeysAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, tool := range aiTools {
		if prev, clash := seen[tool.key]; clash {
			t.Errorf("key %q is used by both %s and %s", tool.key, prev, tool.name)
		}
		seen[tool.key] = tool.name
		if _, ok := aiToolByKey(tool.key); !ok {
			t.Errorf("%s cannot be found by its own key %q", tool.name, tool.key)
		}
	}
	if _, ok := aiToolByKey("z"); ok {
		t.Error("an unknown key should find nothing")
	}
}

// A path with spaces must survive into the pasted command, or it breaks silently.
func TestCommandQuotesAnAwkwardServerName(t *testing.T) {
	got := mcpCommandForTool(aiTools[0], "my db")
	if !strings.Contains(got, "'minidock-my db'") || !strings.Contains(got, "'my db'") {
		t.Errorf("an awkward name should be quoted: %q", got)
	}
}

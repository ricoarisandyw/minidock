package tui

import "strings"

// hint is one "key  action" pair shown in the help bar.
type hint struct{ key, desc string }

func renderHints(hints []hint) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, styleKey.Render(h.key)+" "+styleMuted.Render(h.desc))
	}
	return strings.Join(parts, styleMuted.Render("  ·  "))
}

var (
	hintsTemplates = []hint{
		{"enter", "use"}, {"/", "search"}, {"u", "duplicate"}, {"n", "new"}, {"i", "import"}, {"x", "export"}, {"?", "help"}, {"q", "quit"},
	}
	hintsMine = []hint{
		{"enter", "use"}, {"n", "new"}, {"e", "edit"}, {"u", "duplicate"}, {"d", "delete"}, {"i", "import"}, {"x", "export"}, {"?", "help"}, {"q", "quit"},
	}
	hintsServers = []hint{
		{"enter", "details"}, {"s", "start/stop"}, {"R", "restart"}, {"c", "copy"}, {"m", "mcp"}, {"d", "delete"}, {"r", "refresh"}, {"?", "help"}, {"q", "quit"},
	}
	hintsExternal = []hint{
		{"n", "add"}, {"e", "edit"}, {"t", "test"}, {"m", "mcp"}, {"d", "remove"}, {"r", "refresh"}, {"?", "help"}, {"q", "quit"},
	}
	hintsFilter  = []hint{{"enter", "keep filter"}, {"esc", "clear"}}
	hintsWizard  = []hint{{"enter/tab", "next"}, {"shift+tab", "back"}, {"esc", "cancel"}}
	hintsReview  = []hint{{"enter", "create & start"}, {"esc", "back to form"}}
	hintsDone    = []hint{{"c", "copy connection"}, {"enter", "go to servers"}}
	hintsFailed  = []hint{{"esc", "back"}}
	hintsDetail  = []hint{{"s", "start/stop"}, {"R", "restart"}, {"c", "copy"}, {"m", "mcp"}, {"d", "delete"}, {"↑/↓", "scroll logs"}, {"esc", "back"}}
	hintsEditor  = []hint{{"enter/tab", "next"}, {"shift+tab", "back"}, {"esc", "cancel"}}
	hintsConfirm = []hint{{"esc", "cancel"}}
	hintsPrompt  = []hint{{"enter", "ok"}, {"esc", "cancel"}}
)

// helpText is the full help screen opened with "?".
const helpText = `MiniDock — run Docker servers from templates

Move around
  tab / → / l      next tab          shift+tab / ← / h   previous tab
  ↑ ↓ / k j        move in a list    r                   refresh Docker status
  ?                this help         q                   quit

Templates (and My Templates)
  enter  create a server from the template    /  search
  u      duplicate as a new custom template   n  new template
  e      edit custom template                 d  delete custom template
  i      import a template JSON file          x  export a template to JSON

Servers
  enter  details & live logs                  s  start / stop
  R      restart                              c  copy connection string
  m      copy the command that lets Claude read a MySQL/MariaDB server (read-only)
  d      delete (asks about data)

External — databases MiniDock did not create
  Point MiniDock at a MySQL/MariaDB container you already run (docker compose, a
  colleague's stack) so Claude can read it. Access stays read-only.
  n      add a connection                     e / enter  edit
  t      test it now                          m          copy the command for Claude
  d      remove the saved connection (the container is left alone)

Press any key to close this help.`

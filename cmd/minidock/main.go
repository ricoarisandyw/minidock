// Command minidock is a terminal app for running Docker servers from templates.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"minidock/internal/config"
	"minidock/internal/docker"
	"minidock/internal/external"
	"minidock/internal/mcpserver"
	"minidock/internal/template"
	"minidock/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCP(os.Args[2:])
		return
	}

	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "MiniDock — run Docker servers from templates in an interactive terminal UI.")
		fmt.Fprintln(os.Stderr, "\nUsage: minidock [-version]")
		fmt.Fprintln(os.Stderr, "       minidock mcp [--server <name>]   read-only MySQL/MariaDB MCP server (stdio)")
		fmt.Fprintln(os.Stderr, "\n<name> is a MiniDock server, or a connection to an existing database saved on the External tab.")
		fmt.Fprintln(os.Stderr, "\nEnvironment:")
		fmt.Fprintln(os.Stderr, "  DOCKER_HOST     talk to a remote Docker (e.g. ssh://user@host)")
		fmt.Fprintln(os.Stderr, "  MINIDOCK_HOME   folder for your custom templates")
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("minidock", version)
		return
	}

	dir, err := config.TemplatesDir()
	if err != nil {
		fatal(err)
	}
	store := template.NewStore(dir)

	extDir, err := config.ExternalDir()
	if err != nil {
		fatal(err)
	}
	conns := external.NewStore(extDir)

	dc, err := docker.New()
	if err != nil {
		fatal(fmt.Errorf("cannot prepare the Docker client: %w", err))
	}
	defer dc.Close()

	p := tea.NewProgram(tui.New(dc, store, conns), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fatal(err)
	}
}

// runMCP starts the read-only MySQL MCP server. stdout is the protocol channel,
// so anything meant for people goes to stderr.
func runMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	server := fs.String("server", "", "name of the MiniDock server or saved connection (default: the only one available)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: minidock mcp [--server <name>]")
		fmt.Fprintln(os.Stderr, "\nA read-only MCP server for a MySQL/MariaDB database: one MiniDock runs, or an existing")
		fmt.Fprintln(os.Stderr, "one saved as a connection on the External tab. It talks over stdin/stdout.")
		fmt.Fprintln(os.Stderr, "Add it to Claude Code with:  claude mcp add minidock-mysql -- minidock mcp --server <name>")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	dc, err := docker.New()
	if err != nil {
		fatal(fmt.Errorf("cannot prepare the Docker client: %w", err))
	}
	defer dc.Close()

	// Saved connections are optional: without them only MiniDock's own servers are
	// reachable. A nil ConnSource must stay nil in the interface, not a nil pointer.
	var conns mcpserver.ConnSource
	if extDir, err := config.ExternalDir(); err == nil {
		conns = external.NewStore(extDir)
	}

	// A person who runs this by hand sees nothing otherwise: it just waits for an MCP client.
	// The note goes to stderr, never stdout, which belongs to the protocol.
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintln(os.Stderr, "minidock mcp is running and waiting for an MCP client (it is not meant to be used by hand).")
		fmt.Fprintln(os.Stderr, "Add it to Claude Code instead:  claude mcp add minidock-mysql -- minidock mcp --server <name>")
		fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpserver.New(dc, conns, *server, version).Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "minidock:", err)
	os.Exit(1)
}

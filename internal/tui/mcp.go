package tui

import (
	"os"
	"strings"

	"minidock/internal/docker"
)

// mcpCommand is the line a person pastes in a terminal to give Claude Code
// read-only access to a MySQL/MariaDB server. It uses the path of this very program.
func mcpCommand(inst docker.Instance) string { return mcpCommandFor(inst.Name) }

// mcpCommandFor builds that line for any server or saved connection name.
func mcpCommandFor(name string) string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "minidock"
	}
	return "claude mcp add " + shellQuote("minidock-"+name) +
		" -- " + shellQuote(exe) + " mcp --server " + shellQuote(name)
}

// shellQuote leaves simple words alone and single-quotes anything else.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

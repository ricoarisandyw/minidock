package tui

import (
	"minidock/internal/docker"
)

// mcpCommand builds the Claude Code line for a server. It is what the older
// tests and callers expect; the dialog uses mcpCommandForTool directly.
func mcpCommand(inst docker.Instance) string { return mcpCommandFor(inst.Name) }

// mcpCommandFor builds the Claude Code line for any server or connection name.
func mcpCommandFor(name string) string {
	return mcpCommandForTool(aiTools[0], name)
}

package tui

import (
	"os"
	"strings"
)

// aiTool is an AI coding tool that can be given this MCP server.
type aiTool struct {
	// key is the single key that picks it in the dialog.
	key string
	// name is what the person sees.
	name string
	// program is the CLI that registers MCP servers.
	program string
}

// aiTools are the tools MiniDock can write a command for. All three register a
// stdio MCP server the same way: `<program> mcp add <name> -- <command> <args>`.
// The `--` matters: it stops the tool from reading our own arguments as its own,
// and the path to this program may hold spaces or dashes.
var aiTools = []aiTool{
	{key: "c", name: "Claude Code", program: "claude"},
	{key: "x", name: "Codex", program: "codex"},
	{key: "g", name: "Gemini CLI", program: "gemini"},
}

// aiToolByKey finds a tool by the key pressed in the dialog.
func aiToolByKey(key string) (aiTool, bool) {
	for _, t := range aiTools {
		if t.key == key {
			return t, true
		}
	}
	return aiTool{}, false
}

// mcpCommandForTool is the line a person pastes in a terminal to give one AI tool
// read-only access to the server or saved connection called name.
func mcpCommandForTool(t aiTool, name string) string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "minidock"
	}
	return t.program + " mcp add " + shellQuote("minidock-"+name) +
		" -- " + shellQuote(exe) + " mcp --server " + shellQuote(name)
}

// shellQuote leaves simple words alone and single-quotes anything else.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

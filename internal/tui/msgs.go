package tui

import (
	"minidock/internal/docker"
	"minidock/internal/external"
	"minidock/internal/template"
)

// Messages passed through Bubble Tea's update loop.
type (
	tickMsg struct{}

	statusMsg struct{ status docker.Status }

	instancesMsg struct {
		list []docker.Instance
		used map[int]bool
		err  error
	}

	// actionMsg is the result of start/stop/restart/delete.
	actionMsg struct {
		label string
		err   error
	}

	toastClearMsg struct{ seq int }

	// serverActionMsg is sent by the servers list and detail view.
	// kind is one of: toggle, restart, delete, copy.
	serverActionMsg struct {
		kind string
		inst docker.Instance
	}

	// externalActionMsg is sent by the list of saved connections.
	// kind is one of: new, edit, delete, mcp, test.
	externalActionMsg struct {
		kind string
		conn external.Conn
	}

	// connFormDoneMsg: the connection form finished. saved is false when cancelled.
	connFormDoneMsg struct {
		conn  external.Conn
		isNew bool
		saved bool
	}

	// foreignMsg carries the containers MiniDock did not create, for the pick list.
	foreignMsg struct {
		list []docker.Foreign
		err  error
	}

	// connTestMsg is the result of trying a saved connection.
	connTestMsg struct {
		name    string
		summary string
		err     error
	}

	// wizardClosedMsg: the wizard ended; created tells whether a server was made.
	wizardClosedMsg struct{ created bool }

	// detailClosedMsg: the detail view was closed.
	detailClosedMsg struct{}

	// editorDoneMsg: the editor finished. saved is false when cancelled.
	editorDoneMsg struct {
		tpl   template.Template
		isNew bool
		saved bool
	}

	// confirmChoiceMsg: a confirm dialog was answered. value is "" when cancelled.
	confirmChoiceMsg struct{ id, value string }

	// promptDoneMsg: a text prompt was answered.
	promptDoneMsg struct {
		id     string
		value  string
		cancel bool
	}

	createEventMsg struct{ ev createEvent }

	logsOpenedMsg struct {
		id     string
		stream *docker.LogStream
		err    error
	}

	logLineMsg struct {
		id   string
		line string
		ok   bool // false when the stream ended
	}
)

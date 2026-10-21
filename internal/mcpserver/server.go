package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Backend is what the server needs from Docker. *docker.Client implements it.
type Backend interface {
	Lister
	Runner
	Finder
}

// Server answers MCP requests, one JSON message per line.
type Server struct {
	be      Backend
	conns   ConnSource // saved connections to databases MiniDock does not manage; may be nil
	name    string     // server or connection to use; "" means the only one available
	version string
	tools   []tool
}

// New creates a server. name may be empty to use the only available MySQL/MariaDB
// server. conns may be nil when no external connections are saved.
func New(be Backend, conns ConnSource, name, version string) *Server {
	return &Server{be: be, conns: conns, name: name, version: version, tools: tools()}
}

// Protocol versions we know. The client's version is echoed back when it is one of them.
var supportedVersions = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

const latestVersion = "2025-06-18"

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from in and writes responses to out until in ends or ctx is done.
// Nothing but protocol messages may be written to out.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		r := bufio.NewReader(in)
		for {
			line, err := r.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				lines <- line
			}
			if err != nil {
				if err != io.EOF {
					readErr <- err
				}
				return
			}
		}
	}()

	enc := json.NewEncoder(out)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return err
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			if resp := s.handle(ctx, line); resp != nil {
				if err := enc.Encode(resp); err != nil {
					return err
				}
			}
		}
	}
}

// handle returns the response for one message, or nil for notifications.
func (s *Server) handle(ctx context.Context, line []byte) *response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return fail(json.RawMessage("null"), -32700, "invalid JSON")
	}
	if len(req.ID) == 0 { // a notification such as notifications/initialized
		return nil
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := latestVersion
		if supportedVersions[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		return ok(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "minidock-mysql-readonly", "version": s.version},
			"instructions":    "Read-only access to a MySQL/MariaDB server, either one MiniDock runs or an existing database it was pointed at. Writes are refused.",
		})
	case "ping":
		return ok(req.ID, map[string]any{})
	case "tools/list":
		return ok(req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		return s.call(ctx, req)
	default:
		return fail(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) call(ctx context.Context, req request) *response {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return fail(req.ID, -32602, "invalid parameters")
	}
	for _, t := range s.tools {
		if t.Name != p.Name {
			continue
		}
		res, err := t.run(ctx, s, p.Arguments)
		if err != nil {
			return ok(req.ID, toolText(err.Error(), true))
		}
		body, err := json.Marshal(res)
		if err != nil {
			return ok(req.ID, toolText(err.Error(), true))
		}
		return ok(req.ID, toolText(string(body), false))
	}
	return fail(req.ID, -32602, "unknown tool: "+p.Name)
}

// exec finds the target server (it may have been stopped since the last call) and runs sql.
func (s *Server) exec(ctx context.Context, sql string, limit int) (Result, error) {
	t, err := Discover(ctx, s.be, s.conns, s.name)
	if err != nil {
		return Result{}, err
	}
	return runSQL(ctx, s.be, t, sql, limit)
}

func toolText(text string, isError bool) map[string]any {
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isError {
		out["isError"] = true
	}
	return out
}

func ok(id json.RawMessage, result any) *response {
	return &response{JSONRPC: "2.0", ID: id, Result: result}
}

func fail(id json.RawMessage, code int, msg string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// Probe tries the target the way a tool call would and describes what it found.
// It is what the app's "test" key uses, so a connection is checked by the very
// code path the AI will take, not by a lookalike.
func (s *Server) Probe(ctx context.Context) (string, error) {
	t, err := Discover(ctx, s.be, s.conns, s.name)
	if err != nil {
		return "", err
	}
	res, err := runSQL(ctx, s.be, t, "SHOW DATABASES", MaxRowLimit)
	if err != nil {
		return "", err
	}
	label := t.Label
	if t.Database != "" {
		label += " (default database " + t.Database + ")"
	}
	return fmt.Sprintf("%s works: %s can read %d database(s).", label, t.User, res.RowCount), nil
}

// Package mcpserver is a read-only MCP (Model Context Protocol) server for the
// MySQL and MariaDB servers MiniDock runs. It talks JSON-RPC over stdin/stdout.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"minidock/internal/docker"
	"minidock/internal/external"
)

// Target is the MySQL/MariaDB server the tools talk to. It is found afresh for
// every call, so a container that was restarted or recreated is picked up on its own.
type Target struct {
	// ContainerID is the container the SQL client runs in.
	ContainerID string
	// Label names the server in messages: a MiniDock server name, or the name
	// of a saved connection to a database MiniDock does not manage.
	Label    string
	Client   string // "mysql" or "mariadb": the SQL program inside the container
	User     string
	Password string
	Database string
}

// Lister is what discovery needs from Docker. *docker.Client implements it.
type Lister interface {
	List(ctx context.Context) ([]docker.Instance, error)
}

// Finder looks up a container MiniDock did not create. *docker.Client implements it.
type Finder interface {
	FindContainer(ctx context.Context, nameOrID string) (docker.Foreign, error)
}

// ConnSource holds the saved connections to databases MiniDock does not manage.
// *external.Store implements it. Discovery works without one.
type ConnSource interface {
	Get(name string) (external.Conn, bool)
	Names() []string
}

// Discover finds the MySQL/MariaDB server to read. name is either a MiniDock
// server or a saved connection to a database MiniDock does not manage; an empty
// name picks the only running candidate. cs may be nil when no connections are saved.
// Errors are written to be shown to the person using the AI.
func Discover(ctx context.Context, be Backend, cs ConnSource, name string) (Target, error) {
	all, err := be.List(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("cannot reach Docker: %s", docker.Explain(err))
	}

	if name != "" {
		for _, i := range all {
			if i.Name != name {
				continue
			}
			if i.MySQLClient() == "" {
				return Target{}, fmt.Errorf("%q is not a MySQL or MariaDB server, and this MCP only supports those", name)
			}
			return targetFromInstance(i)
		}
		if cs != nil {
			if c, ok := cs.Get(name); ok {
				return targetFromConn(ctx, be, c)
			}
		}
		return Target{}, fmt.Errorf("no MiniDock server or saved connection named %q. %s", name, availableText(all, cs))
	}

	// No name: there must be exactly one candidate, counting saved connections.
	var running []docker.Instance
	for _, i := range all {
		if i.MySQLClient() != "" && i.Running() {
			running = append(running, i)
		}
	}
	var saved []string
	if cs != nil {
		saved = cs.Names()
	}
	switch {
	case len(running) == 1 && len(saved) == 0:
		return targetFromInstance(running[0])
	case len(running) == 0 && len(saved) == 1:
		c, _ := cs.Get(saved[0])
		return targetFromConn(ctx, be, c)
	case len(running) == 0 && len(saved) == 0:
		return Target{}, errors.New("no running MySQL or MariaDB server found in MiniDock, and no connection saved. " +
			"Start a server, or add a connection on the External tab, then try again")
	}
	names := make([]string, 0, len(running)+len(saved))
	for _, r := range running {
		names = append(names, r.Name)
	}
	names = append(names, saved...)
	sort.Strings(names)
	return Target{}, fmt.Errorf("several servers are available (%s). Start the MCP with --server <name>", strings.Join(names, ", "))
}

// targetFromInstance builds a target for a server MiniDock created.
func targetFromInstance(inst docker.Instance) (Target, error) {
	if !inst.Running() {
		return Target{}, fmt.Errorf("server %q is stopped. Start it in MiniDock (select it and press s), then try again", inst.Name)
	}
	t := Target{ContainerID: inst.ID, Label: inst.Name, Client: inst.MySQLClient()}
	if err := t.readCredentials(inst.Connection); err != nil {
		return Target{}, err
	}
	return t, nil
}

// targetFromConn builds a target for a saved connection, resolving the container
// by name every time so a recreated container (docker compose up) still works.
func targetFromConn(ctx context.Context, f Finder, c external.Conn) (Target, error) {
	ctr, err := f.FindContainer(ctx, c.Container)
	if err != nil {
		return Target{}, fmt.Errorf("connection %q points at container %q, but %s. "+
			"Start it, or edit the connection on the External tab", c.Name, c.Container, strings.TrimSuffix(err.Error(), "."))
	}
	if !ctr.Running() {
		return Target{}, fmt.Errorf("the container %q behind connection %q is not running (%s). Start it, then try again",
			c.Container, c.Name, ctr.StatusText)
	}
	return Target{
		ContainerID: ctr.ID,
		Label:       c.Name,
		Client:      c.Client,
		User:        c.User,
		Password:    c.Password,
		Database:    c.Database,
	}, nil
}

// readCredentials takes user, password and default database from the connection
// string MiniDock saved on the container (mysql://user:pass@host:port/db).
func (t *Target) readCredentials(connection string) error {
	u, err := url.Parse(connection)
	if err != nil || u.User == nil || u.User.Username() == "" {
		return fmt.Errorf("server %q has no usable connection string, so the login cannot be found", t.Label)
	}
	t.User = u.User.Username()
	t.Password, _ = u.User.Password()
	t.Database = strings.TrimPrefix(u.Path, "/")
	if strings.HasPrefix(t.User, "-") {
		return fmt.Errorf("server %q has an unusable user name", t.Label)
	}
	return nil
}

// availableText lists what the person could have meant instead.
func availableText(all []docker.Instance, cs ConnSource) string {
	var names []string
	for _, i := range all {
		if i.MySQLClient() != "" {
			names = append(names, i.Name)
		}
	}
	if cs != nil {
		names = append(names, cs.Names()...)
	}
	if len(names) == 0 {
		return "There are no MySQL or MariaDB servers yet. Create one in MiniDock, or add a connection on the External tab."
	}
	sort.Strings(names)
	return "Available: " + strings.Join(names, ", ") + "."
}

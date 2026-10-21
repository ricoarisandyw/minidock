// Package external holds the MySQL/MariaDB containers that MiniDock did not
// create but may still read: databases from a docker-compose project, a
// colleague's stack, anything already running on the Docker host.
package external

import (
	"fmt"
	"regexp"
	"strings"
)

// Conn is a saved read-only connection to a container MiniDock does not manage.
type Conn struct {
	// Name identifies the connection in `minidock mcp --server <name>`.
	Name string `json:"name"`
	// Container is the Docker container name to exec into. It is resolved
	// afresh on every use, so a recreated container is picked up on its own.
	Container string `json:"container"`
	// Client is the SQL program inside the container: "mysql" or "mariadb".
	Client string `json:"client"`
	// User, Password and Database are the login used for every query.
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"`
	// Note is a free line of text, shown in the list.
	Note string `json:"note,omitempty"`
}

// nameRE keeps connection names usable as file names and as shell words.
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Validate reports why a connection cannot be saved, or nil when it is fine.
func Validate(c Conn) error {
	switch {
	case strings.TrimSpace(c.Name) == "":
		return fmt.Errorf("the connection needs a name")
	case !nameRE.MatchString(c.Name):
		return fmt.Errorf("name %q may only use letters, digits, dot, dash and underscore, and must start with a letter or digit", c.Name)
	case strings.TrimSpace(c.Container) == "":
		return fmt.Errorf("choose the Docker container to read")
	case c.Client != "mysql" && c.Client != "mariadb":
		return fmt.Errorf("client must be %q or %q, not %q", "mysql", "mariadb", c.Client)
	case strings.TrimSpace(c.User) == "":
		return fmt.Errorf("the connection needs a user name")
	case strings.HasPrefix(c.User, "-"):
		return fmt.Errorf("a user name cannot start with %q", "-")
	case strings.HasPrefix(c.Database, "-"):
		return fmt.Errorf("a database name cannot start with %q", "-")
	}
	return nil
}

// Describe is the one-line summary shown in the list.
func (c Conn) Describe() string {
	s := c.User + "@" + c.Container
	if c.Database != "" {
		s += "/" + c.Database
	}
	return s
}

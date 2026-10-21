package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// Foreign is a container MiniDock did not create. MiniDock never changes these:
// it only looks them up and runs read-only SQL inside them.
type Foreign struct {
	ID         string
	Name       string
	Image      string
	State      string
	StatusText string
	// Client is "mysql" or "mariadb" when the image looks like one of those,
	// and "" when it does not.
	Client string
	// Compose is the docker-compose project the container belongs to, if any.
	Compose string
}

// Running reports whether the container is currently up.
func (f Foreign) Running() bool { return f.State == "running" || f.State == "restarting" }

// sqlClientForImage guesses the SQL program inside a container from its image name.
func sqlClientForImage(image string) string {
	name := strings.ToLower(image)
	if n := strings.LastIndex(name, "/"); n >= 0 {
		name = name[n+1:]
	}
	switch {
	case strings.HasPrefix(name, "mariadb"):
		return "mariadb"
	case strings.HasPrefix(name, "mysql"), strings.HasPrefix(name, "percona"):
		return "mysql"
	}
	return ""
}

// ListForeign returns the containers MiniDock did not create, running ones
// first and MySQL/MariaDB-looking ones before the rest, so the pick list in the
// app opens on the containers a person is most likely after.
func (c *Client) ListForeign(ctx context.Context) ([]Foreign, error) {
	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]Foreign, 0, len(list))
	for _, ctr := range list {
		if ctr.Labels[LabelManaged] == "true" {
			continue // MiniDock's own servers already have their own tab
		}
		f := Foreign{
			ID:         ctr.ID,
			Image:      ctr.Image,
			State:      ctr.State,
			StatusText: ctr.Status,
			Client:     sqlClientForImage(ctr.Image),
			Compose:    ctr.Labels["com.docker.compose.project"],
		}
		if len(ctr.Names) > 0 {
			f.Name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Client != "") != (b.Client != "") {
			return a.Client != ""
		}
		if a.Running() != b.Running() {
			return a.Running()
		}
		return a.Name < b.Name
	})
	return out, nil
}

// FindContainer looks up one container by name or id, whoever created it.
// The error text is written to be shown to the person using the AI.
func (c *Client) FindContainer(ctx context.Context, nameOrID string) (Foreign, error) {
	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return Foreign{}, err
	}
	for _, ctr := range list {
		var name string
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		if name != nameOrID && ctr.ID != nameOrID && !strings.HasPrefix(ctr.ID, nameOrID) {
			continue
		}
		return Foreign{
			ID:         ctr.ID,
			Name:       name,
			Image:      ctr.Image,
			State:      ctr.State,
			StatusText: ctr.Status,
			Client:     sqlClientForImage(ctr.Image),
			Compose:    ctr.Labels["com.docker.compose.project"],
		}, nil
	}
	return Foreign{}, fmt.Errorf("no Docker container named %q is on this machine", nameOrID)
}

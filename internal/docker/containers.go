package docker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"

	"minidock/internal/template"
)

// Labels MiniDock puts on the containers it creates, so it can find them again.
const (
	LabelManaged       = "minidock.managed"
	LabelTemplate      = "minidock.template"
	LabelVersion       = "minidock.version"
	LabelConnection    = "minidock.connection"
	LabelHostPort      = "minidock.port"
	LabelContainerPort = "minidock.containerPort"
	LabelVolume        = "minidock.volume"
)

// Instance is a container created by MiniDock.
type Instance struct {
	ID            string
	Name          string
	TemplateID    string
	Version       string
	Image         string
	State         string // running, exited, created, paused, restarting...
	StatusText    string // e.g. "Up 3 minutes"
	HostPort      int
	ContainerPort int
	Volume        string
	Connection    string
	Created       time.Time
}

// Running reports whether the container is currently up.
func (i Instance) Running() bool { return i.State == "running" || i.State == "restarting" }

// MySQLClient returns the SQL client program inside the container ("mysql" or
// "mariadb"), or "" when the server is not a MySQL/MariaDB one.
func (i Instance) MySQLClient() string {
	image := strings.ToLower(i.Image)
	if n := strings.LastIndex(image, "/"); n >= 0 {
		image = image[n+1:]
	}
	switch {
	case i.TemplateID == "mariadb" || strings.HasPrefix(image, "mariadb"):
		return "mariadb"
	case i.TemplateID == "mysql" || strings.HasPrefix(image, "mysql"):
		return "mysql"
	}
	return ""
}

// List returns the containers MiniDock created (running or not), newest first.
func (c *Client) List(ctx context.Context) ([]Instance, error) {
	list, err := c.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", LabelManaged+"=true")),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(list))
	for _, ctr := range list {
		inst := Instance{
			ID:            ctr.ID,
			Image:         ctr.Image,
			State:         ctr.State,
			StatusText:    ctr.Status,
			TemplateID:    ctr.Labels[LabelTemplate],
			Version:       ctr.Labels[LabelVersion],
			Connection:    ctr.Labels[LabelConnection],
			Volume:        ctr.Labels[LabelVolume],
			HostPort:      atoi(ctr.Labels[LabelHostPort]),
			ContainerPort: atoi(ctr.Labels[LabelContainerPort]),
			Created:       time.Unix(ctr.Created, 0),
		}
		if len(ctr.Names) > 0 {
			inst.Name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		if inst.HostPort == 0 {
			for _, p := range ctr.Ports {
				if p.PublicPort != 0 {
					inst.HostPort = int(p.PublicPort)
					inst.ContainerPort = int(p.PrivatePort)
					break
				}
			}
		}
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// UsedPorts returns host ports taken by any container, plus ports reserved by
// stopped MiniDock servers (so a stopped server can always start again).
func (c *Client) UsedPorts(ctx context.Context) (map[int]bool, error) {
	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, ctr := range list {
		for _, p := range ctr.Ports {
			if p.PublicPort != 0 {
				used[int(p.PublicPort)] = true
			}
		}
		if p := atoi(ctr.Labels[LabelHostPort]); p != 0 {
			used[p] = true
		}
	}
	return used, nil
}

// Create pulls the image if needed, then creates and starts the container.
// Progress is reported through onProgress (may be nil). It returns the container id.
func (c *Client) Create(ctx context.Context, spec template.RunSpec, onProgress func(Progress)) (string, error) {
	report := func(p Progress) {
		if onProgress != nil {
			onProgress(p)
		}
	}

	if err := c.ensureImage(ctx, spec.Image, report); err != nil {
		return "", err
	}

	report(Progress{Status: "Creating container…", Percent: -1})
	containerPort := nat.Port(fmt.Sprintf("%d/tcp", spec.ContainerPort))
	labels := map[string]string{
		LabelManaged:       "true",
		LabelTemplate:      spec.TemplateID,
		LabelVersion:       spec.Version,
		LabelConnection:    spec.Connection,
		LabelHostPort:      strconv.Itoa(spec.HostPort),
		LabelContainerPort: strconv.Itoa(spec.ContainerPort),
		LabelVolume:        spec.Volume,
	}
	cfg := &container.Config{
		Image:        spec.Image,
		Env:          spec.Env,
		Cmd:          spec.Cmd,
		Labels:       labels,
		ExposedPorts: nat.PortSet{containerPort: struct{}{}},
	}
	host := &container.HostConfig{
		// Bound to localhost only, so a dev database is not exposed to the network.
		PortBindings: nat.PortMap{containerPort: {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(spec.HostPort)}}},
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	}
	if spec.Volume != "" && spec.DataPath != "" {
		host.Mounts = []mount.Mount{{
			Type:   mount.TypeVolume,
			Source: spec.Volume,
			Target: spec.DataPath,
			VolumeOptions: &mount.VolumeOptions{
				Labels: map[string]string{LabelManaged: "true"},
			},
		}}
	}

	created, err := c.cli.ContainerCreate(ctx, cfg, host, nil, nil, spec.Name)
	if err != nil {
		return "", err
	}
	report(Progress{Status: "Starting…", Percent: -1})
	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		// Do not leave a half-made container behind.
		_ = c.cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
		return "", err
	}
	report(Progress{Status: "Running", Percent: 1})
	return created.ID, nil
}

// Start starts a stopped container.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.cli.ContainerStart(ctx, id, container.StartOptions{})
}

// Stop stops a running container.
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.cli.ContainerStop(ctx, id, container.StopOptions{})
}

// Restart restarts a container.
func (c *Client) Restart(ctx context.Context, id string) error {
	return c.cli.ContainerRestart(ctx, id, container.StopOptions{})
}

// Remove deletes a container. With removeData it also deletes the data volume.
func (c *Client) Remove(ctx context.Context, inst Instance, removeData bool) error {
	if err := c.cli.ContainerRemove(ctx, inst.ID, container.RemoveOptions{Force: true}); err != nil {
		return err
	}
	if removeData && inst.Volume != "" {
		return c.cli.VolumeRemove(ctx, inst.Volume, true)
	}
	return nil
}

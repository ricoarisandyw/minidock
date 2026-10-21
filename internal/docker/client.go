// Package docker is a thin wrapper around the Docker Engine SDK,
// limited to what MiniDock needs.
package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/docker/docker/client"
)

// Client talks to the Docker daemon (local, or remote via DOCKER_HOST).
type Client struct {
	cli *client.Client
}

// New creates a client from the environment (DOCKER_HOST etc.).
// It does not contact the daemon yet; use Ping for that.
func New() (*Client, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if os.Getenv("DOCKER_HOST") == "" {
		if host := fallbackHost(); host != "" {
			opts = append(opts, client.WithHost(host))
		}
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

// fallbackHost finds a Docker socket in the usual per-user places when the
// default /var/run/docker.sock is missing (newer Docker Desktop, Colima, Rancher).
func fallbackHost() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, p := range []string{
		filepath.Join(home, ".docker", "run", "docker.sock"),
		filepath.Join(home, ".colima", "default", "docker.sock"),
		filepath.Join(home, ".rd", "docker.sock"),
	} {
		if _, err := os.Stat(p); err == nil {
			return "unix://" + p
		}
	}
	return ""
}

// Status is the result of checking the Docker connection.
type Status struct {
	Connected bool
	Version   string
	Hint      string // short advice shown when not connected
	Err       error
}

// Ping checks whether Docker is installed and running.
func (c *Client) Ping(ctx context.Context) Status {
	v, err := c.cli.ServerVersion(ctx)
	if err == nil {
		return Status{Connected: true, Version: v.Version}
	}
	hint := "Docker is not running. Start Docker Desktop (or the Docker service), then press r."
	if os.Getenv("DOCKER_HOST") == "" {
		if _, lookErr := exec.LookPath("docker"); lookErr != nil {
			hint = "Docker does not seem to be installed. Install Docker Desktop, then press r."
		}
	} else {
		hint = "Cannot reach Docker at " + os.Getenv("DOCKER_HOST") + ". Check the address, then press r."
	}
	return Status{Hint: hint, Err: err}
}

// Close releases the connection.
func (c *Client) Close() error { return c.cli.Close() }

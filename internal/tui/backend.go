// Package tui is the interactive terminal interface of MiniDock.
package tui

import (
	"context"

	"minidock/internal/docker"
	"minidock/internal/template"
)

// Backend is what the TUI needs from Docker. *docker.Client implements it,
// and tests use a fake.
type Backend interface {
	Ping(ctx context.Context) docker.Status
	List(ctx context.Context) ([]docker.Instance, error)
	UsedPorts(ctx context.Context) (map[int]bool, error)
	Create(ctx context.Context, spec template.RunSpec, onProgress func(docker.Progress)) (string, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
	Remove(ctx context.Context, inst docker.Instance, removeData bool) error
	Logs(ctx context.Context, id string, tail int) (*docker.LogStream, error)
	// ListForeign and FindContainer cover the databases MiniDock did not create.
	ListForeign(ctx context.Context) ([]docker.Foreign, error)
	FindContainer(ctx context.Context, nameOrID string) (docker.Foreign, error)
	Exec(ctx context.Context, id string, cmd, env []string, maxOutput int) (docker.ExecResult, error)
}

var _ Backend = (*docker.Client)(nil)

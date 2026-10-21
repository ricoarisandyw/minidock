package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ErrOutputTooLarge is returned by Exec when a command prints more than maxOutput bytes.
var ErrOutputTooLarge = errors.New("command output is too large")

// ExecResult is what a command run inside a container printed.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Exec runs cmd inside a running container and waits for it to finish.
// env entries look like "KEY=value". Output is capped at maxOutput bytes per stream;
// going over it returns ErrOutputTooLarge.
func (c *Client) Exec(ctx context.Context, id string, cmd, env []string, maxOutput int) (ExecResult, error) {
	created, err := c.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          cmd,
		Env:          env,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, err
	}
	att, err := c.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, err
	}
	defer att.Close()

	// Closing the connection when the context ends unblocks the read below.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			att.Close()
		case <-done:
		}
	}()

	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = maxOutput, maxOutput
	_, copyErr := stdcopy.StdCopy(&stdout, &stderr, att.Reader)
	if ctx.Err() != nil {
		return ExecResult{}, ctx.Err()
	}
	if stdout.over || stderr.over {
		return ExecResult{}, ErrOutputTooLarge
	}
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return ExecResult{}, copyErr
	}

	info, err := c.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return ExecResult{}, fmt.Errorf("cannot read the command result: %w", err)
	}
	return ExecResult{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes(), ExitCode: info.ExitCode}, nil
}

// limitedBuffer collects output until max bytes, then keeps draining but remembers it was too much.
type limitedBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.over || l.buf.Len()+len(p) > l.max {
		l.over = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

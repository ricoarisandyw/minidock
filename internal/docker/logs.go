package docker

import (
	"bufio"
	"context"
	"io"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// LogStream delivers log lines until the container stops or Close is called.
type LogStream struct {
	Lines  <-chan string
	cancel context.CancelFunc
}

// Close stops following the logs.
func (s *LogStream) Close() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

// Logs follows a container's logs, starting with the last `tail` lines.
func (c *Client) Logs(ctx context.Context, id string, tail int) (*LogStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	rc, err := c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		cancel()
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		// Containers made by MiniDock have no TTY, so the stream is multiplexed.
		_, err := stdcopy.StdCopy(pw, pw, rc)
		_ = rc.Close()
		_ = pw.CloseWithError(err)
	}()

	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				_ = pr.Close()
				return
			}
		}
	}()
	return &LogStream{Lines: lines, cancel: cancel}, nil
}

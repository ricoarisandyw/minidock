//go:build integration

package docker

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"minidock/internal/template"
)

// Run with: go test -tags integration ./internal/docker
// It needs a running Docker, pulls redis:7-alpine and cleans up after itself.
func TestIntegrationRedisLifecycle(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if st := c.Ping(ctx); !st.Connected {
		t.Skipf("Docker is not available: %s", st.Hint)
	}

	// Ask the OS for a free port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	name := "minidock-it-" + strconv.FormatInt(time.Now().Unix(), 10)
	spec := template.RunSpec{
		Name: name, TemplateID: "redis", Version: "7-alpine", Image: "redis:7-alpine",
		HostPort: port, ContainerPort: 6379,
		Volume: name + "-data", DataPath: "/data",
		Connection: "redis://localhost:" + strconv.Itoa(port),
	}

	var progressCalls int
	id, err := c.Create(ctx, spec, func(Progress) { progressCalls++ })
	if err != nil {
		t.Fatalf("create: %v", explainFor(err))
	}
	var inst Instance
	t.Cleanup(func() {
		// Always remove the container and its volume, even when an assertion failed.
		_ = c.Remove(context.Background(), Instance{ID: id, Volume: spec.Volume}, true)
	})
	if progressCalls == 0 {
		t.Error("expected progress updates")
	}

	list, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range list {
		if i.ID == id {
			inst = i
		}
	}
	if inst.ID == "" {
		t.Fatal("created container not listed")
	}
	if !inst.Running() || inst.HostPort != port || inst.TemplateID != "redis" || inst.Volume != spec.Volume || inst.Connection != spec.Connection {
		t.Errorf("unexpected instance: %+v", inst)
	}

	used, err := c.UsedPorts(ctx)
	if err != nil || !used[port] {
		t.Errorf("port %d should be reported as used (err=%v)", port, err)
	}

	if err := c.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	list, _ = c.List(ctx)
	for _, i := range list {
		if i.ID == id && i.Running() {
			t.Error("container should be stopped")
		}
	}
	// A stopped server still reserves its port.
	if used, _ := c.UsedPorts(ctx); !used[port] {
		t.Error("stopped server should still reserve its port")
	}

	if err := c.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.Restart(ctx, id); err != nil {
		t.Fatal(err)
	}

	logs, err := c.Logs(ctx, id, 50)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case line, ok := <-logs.Lines:
		if !ok || line == "" {
			t.Errorf("expected a log line, got %q ok=%v", line, ok)
		}
	case <-time.After(10 * time.Second):
		t.Error("no log output within 10s")
	}
	logs.Close()

	if err := c.Remove(ctx, Instance{ID: id, Volume: spec.Volume}, true); err != nil {
		t.Fatal(err)
	}
	list, _ = c.List(ctx)
	for _, i := range list {
		if i.ID == id {
			t.Error("container should be gone")
		}
	}
}

func explainFor(err error) string { return Explain(err) + " (" + err.Error() + ")" }

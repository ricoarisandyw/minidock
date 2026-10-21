package docker

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExplainFriendlyMessages(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"), "not running"},
		{errors.New("Error response from daemon: driver failed programming external connectivity: Bind for 127.0.0.1:5432 failed: port is already allocated"), "port is already in use"},
		{errors.New(`Error response from daemon: Conflict. The container name "/pg" is already in use by container "abc"`), "name already exists"},
		{errors.New("pull access denied for nope, repository does not exist or may require 'docker login'"), "Image or version not found"},
		{errors.New("write: no space left on device"), "no disk space"},
		{context.DeadlineExceeded, "too long"},
		{errors.New("Error response from daemon: something odd"), "something odd"},
	}
	for _, c := range cases {
		got := Explain(c.err)
		if !strings.Contains(strings.ToLower(got), strings.ToLower(c.want)) {
			t.Errorf("Explain(%q) = %q, want it to contain %q", c.err, got, c.want)
		}
	}
	if Explain(nil) != "" {
		t.Error("Explain(nil) should be empty")
	}
	if strings.HasPrefix(Explain(errors.New("Error response from daemon: x")), "Error response") {
		t.Error("daemon prefix should be stripped")
	}
}

func TestReadPullReportsOverallProgress(t *testing.T) {
	stream := `{"status":"Pulling from library/redis","id":"7"}
{"status":"Downloading","id":"a1","progressDetail":{"current":50,"total":100}}
{"status":"Downloading","id":"b2","progressDetail":{"current":0,"total":100}}
{"status":"Downloading","id":"b2","progressDetail":{"current":100,"total":100}}
{"status":"Download complete","id":"a1","progressDetail":{"current":100,"total":100}}
`
	var last Progress
	var calls int
	err := readPull(strings.NewReader(stream), "redis:7", func(p Progress) { last, calls = p, calls+1 })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Errorf("calls = %d, want 5", calls)
	}
	if last.Percent != 1 {
		t.Errorf("final percent = %v, want 1", last.Percent)
	}
}

func TestReadPullSurfacesErrors(t *testing.T) {
	err := readPull(strings.NewReader(`{"error":"manifest unknown"}`), "x:1", func(Progress) {})
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Errorf("got %v", err)
	}
}

func TestInstanceRunning(t *testing.T) {
	for state, want := range map[string]bool{"running": true, "restarting": true, "exited": false, "created": false, "paused": false} {
		if got := (Instance{State: state}).Running(); got != want {
			t.Errorf("%s: got %v, want %v", state, got, want)
		}
	}
}

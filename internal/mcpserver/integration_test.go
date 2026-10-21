//go:build integration

package mcpserver

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"minidock/internal/docker"
	"minidock/internal/template"
)

// Run with: go test -tags integration -count=1 -v ./internal/mcpserver
// It needs a running Docker, pulls mysql:8.4 and cleans up after itself.
func TestIntegrationMySQLReadOnly(t *testing.T) {
	c, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if st := c.Ping(ctx); !st.Connected {
		t.Skipf("Docker is not available: %s", st.Hint)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	name := "minidock-mcp-it-" + strconv.FormatInt(time.Now().Unix(), 10)
	spec := template.RunSpec{
		Name: name, TemplateID: "mysql", Version: "8.4", Image: "mysql:8.4",
		HostPort: port, ContainerPort: 3306,
		Volume: name + "-data", DataPath: "/var/lib/mysql",
		Env:        []string{"MYSQL_ROOT_PASSWORD=p@ss:word", "MYSQL_DATABASE=app"},
		Connection: "mysql://root:p%40ss%3Aword@localhost:" + strconv.Itoa(port) + "/app",
	}
	id, err := c.Create(ctx, spec, nil)
	if err != nil {
		t.Fatalf("create: %s (%v)", docker.Explain(err), err)
	}
	t.Cleanup(func() {
		_ = c.Remove(context.Background(), docker.Instance{ID: id, Volume: spec.Volume}, true)
	})

	srv := New(c, name, "it")
	call := func(tool, args string) (string, bool) {
		t.Helper()
		line := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}` + "\n"
		var out strings.Builder
		if err := srv.Serve(ctx, strings.NewReader(line), &out); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Result struct {
				Content []struct{ Text string } `json:"content"`
				IsError bool                    `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(out.String()), &resp); err != nil || len(resp.Result.Content) == 0 {
			t.Fatalf("bad response %q: %v", out.String(), err)
		}
		return resp.Result.Content[0].Text, resp.Result.IsError
	}

	// The image restarts its server once while initialising, so wait until queries work.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if _, isErr := call("list_databases", `{}`); !isErr {
			break
		}
		if time.Now().After(deadline) {
			text, _ := call("list_databases", `{}`)
			t.Fatalf("MySQL never became ready: %s", text)
		}
		time.Sleep(3 * time.Second)
	}

	target, err := Discover(ctx, c, name)
	if err != nil {
		t.Fatal(err)
	}

	// Set up data the way a person would: through the client, with full rights.
	setup := "CREATE TABLE app.users (id INT PRIMARY KEY, name VARCHAR(50) NULL); " +
		"INSERT INTO app.users VALUES (1,'Ann'),(2,NULL),(3,'Zoë ünï');"
	res, err := c.Exec(ctx, target.Instance.ID,
		[]string{"mysql", "--user=root", "-e", setup}, []string{"MYSQL_PWD=" + target.Password}, 1<<20)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("setup failed: %v %s", err, res.Stderr)
	}

	if text, isErr := call("list_databases", `{}`); isErr || !strings.Contains(text, `"app"`) {
		t.Errorf("list_databases: %s", text)
	}
	if text, isErr := call("list_tables", `{}`); isErr || !strings.Contains(text, "users") {
		t.Errorf("list_tables: %s", text)
	}
	if text, isErr := call("describe_table", `{"table":"users"}`); isErr || !strings.Contains(text, "varchar(50)") {
		t.Errorf("describe_table: %s", text)
	}
	text, isErr := call("query", `{"sql":"SELECT id, name FROM users ORDER BY id"}`)
	if isErr {
		t.Fatalf("query: %s", text)
	}
	var got Result
	if err := json.Unmarshal([]byte(text), &got); err != nil || got.RowCount != 3 {
		t.Fatalf("query result: %s (%v)", text, err)
	}
	if got.Rows[1][1] != nil || *got.Rows[2][1] != "Zoë ünï" {
		t.Errorf("NULL or unicode not preserved: %s", text)
	}
	if text, _ := call("query", `{"sql":"SELECT id FROM users","limit":1}`); !strings.Contains(text, `"truncated":true`) {
		t.Errorf("limit not applied: %s", text)
	}

	// Layer 1: the check refuses writes.
	if text, isErr := call("query", `{"sql":"DELETE FROM users"}`); !isErr || !strings.Contains(text, "refused") {
		t.Errorf("DELETE should be refused: %s", text)
	}
	// Layer 2: even with the check bypassed, MySQL refuses the write.
	_, err = runSQL(ctx, c, target, "DELETE FROM users", 10)
	if err == nil || !strings.Contains(err.Error(), "READ ONLY") {
		t.Errorf("MySQL should refuse writes in the read-only session, got %v", err)
	}
	_, err = runSQL(ctx, c, target, "CREATE TABLE app.sneaky (a INT)", 10)
	if err == nil {
		t.Error("MySQL should refuse CREATE TABLE in the read-only session")
	}

	// Nothing changed.
	text, _ = call("query", `{"sql":"SELECT COUNT(*) AS n FROM users"}`)
	if !strings.Contains(text, `"3"`) {
		t.Errorf("data was changed: %s", text)
	}

	// A long query is stopped by the time limit.
	start := time.Now()
	call("query", `{"sql":"SELECT SLEEP(60)"}`)
	if time.Since(start) > 30*time.Second {
		t.Errorf("SLEEP(60) ran for %s, the timeout did not work", time.Since(start))
	}

	// A stopped server gives a friendly message.
	if err := c.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if text, isErr := call("list_databases", `{}`); !isErr || !strings.Contains(text, "stopped") {
		t.Errorf("stopped server: %s", text)
	}
}

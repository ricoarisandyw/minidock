package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"minidock/internal/docker"
	"minidock/internal/external"
)

func TestCheckReadOnlyAllowsReads(t *testing.T) {
	for _, q := range []string{
		"SELECT 1",
		"select * from users where name = 'Bob; DROP TABLE x' limit 5;",
		"SHOW TABLES",
		"show full columns from `my-table`",
		"DESCRIBE users",
		"desc users",
		"EXPLAIN SELECT * FROM users",
		"SELECT `update`, 'into' AS \"for\" FROM t",
		"SELECT 'it\\'s fine' ; ",
		"SELECT 'a''b'",
		"SELECT count(*) FROM orders WHERE note = 'a -- b'",
	} {
		if err := CheckReadOnly(q); err != nil {
			t.Errorf("%q should be allowed: %v", q, err)
		}
	}
}

func TestCheckReadOnlyRefusesRisky(t *testing.T) {
	for _, q := range []string{
		"",
		"   ",
		"INSERT INTO t VALUES (1)",
		"update t set a = 1",
		"DELETE FROM t",
		"DROP TABLE t",
		"dRoP table t",
		"ALTER TABLE t ADD c INT",
		"TRUNCATE t",
		"GRANT ALL ON *.* TO x",
		"SET GLOBAL read_only = 0",
		"LOAD DATA INFILE '/x' INTO TABLE t",
		"CREATE TABLE t (a int)",
		"REPLACE INTO t VALUES (1)",
		"CALL do_things()",
		"USE other",
		"SELECT 1; DROP TABLE x",
		"SELECT 1; SELECT 2",
		"SELECT 1;;",
		"SELECT * FROM t INTO OUTFILE '/tmp/x'",
		"SELECT * FROM t INTO DUMPFILE '/tmp/x'",
		"SELECT 1 INTO @x",
		"SELECT LOAD_FILE('/etc/passwd')",
		"SELECT * FROM t FOR UPDATE",
		"SELECT * FROM t LOCK IN SHARE MODE",
		"EXPLAIN ANALYZE SELECT * FROM t",
		"SELECT 1 /* hi */",
		"SELECT /*!50000 1 */",
		"SELECT 1 -- hi",
		"SELECT 1 # hi",
		"SELECT 1\n\\! ls",
		"SELECT 1 \\G",
		"SELECT 'unclosed",
		"SELECT `unclosed",
		"SELECT 1\x00",
		"WITH x AS (SELECT 1) SELECT * FROM x",
	} {
		if err := CheckReadOnly(q); err == nil {
			t.Errorf("%q should be refused", q)
		}
	}
}

func TestMySQLClientDetection(t *testing.T) {
	cases := map[string]docker.Instance{
		"mysql":   {TemplateID: "mysql", Image: "mysql:8.4"},
		"mariadb": {TemplateID: "mariadb", Image: "mariadb:11"},
		"":        {TemplateID: "redis", Image: "redis:7"},
	}
	for want, inst := range cases {
		if got := inst.MySQLClient(); got != want {
			t.Errorf("%+v: got %q want %q", inst, got, want)
		}
	}
	// A custom template based on a MySQL image is still recognised.
	if got := (docker.Instance{TemplateID: "my-copy", Image: "docker.io/library/mysql:9"}).MySQLClient(); got != "mysql" {
		t.Errorf("custom template: got %q", got)
	}
}

// fake is a Backend with canned servers, canned foreign containers and canned
// command output.
type fake struct {
	servers  []docker.Instance
	foreign  []docker.Foreign
	out      docker.ExecResult
	err      error
	cmd      []string
	env      []string
	lastExec string
	calls    int
}

func (f *fake) List(context.Context) ([]docker.Instance, error) { return f.servers, nil }

func (f *fake) FindContainer(_ context.Context, nameOrID string) (docker.Foreign, error) {
	for _, c := range f.foreign {
		if c.Name == nameOrID || c.ID == nameOrID {
			return c, nil
		}
	}
	return docker.Foreign{}, errors.New("no Docker container named " + nameOrID + " is on this machine")
}

// conns is a ConnSource backed by a plain map.
type conns map[string]external.Conn

func (c conns) Get(name string) (external.Conn, bool) {
	v, ok := c[name]
	return v, ok
}

func (c conns) Names() []string {
	out := make([]string, 0, len(c))
	for n := range c {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
func (f *fake) Exec(_ context.Context, id string, cmd, env []string, _ int) (docker.ExecResult, error) {
	f.calls++
	f.cmd, f.env, f.lastExec = cmd, env, id
	return f.out, f.err
}

func mysqlServer(name string, running bool) docker.Instance {
	state := "exited"
	if running {
		state = "running"
	}
	return docker.Instance{
		ID: "id-" + name, Name: name, TemplateID: "mysql", Image: "mysql:8.4", State: state,
		Connection: "mysql://root:p%40ss%3Aw@localhost:3306/app",
	}
}

func TestDiscover(t *testing.T) {
	redis := docker.Instance{ID: "r", Name: "cache", TemplateID: "redis", Image: "redis:7", State: "running"}
	ctx := context.Background()

	f := &fake{servers: []docker.Instance{mysqlServer("db1", true), mysqlServer("db2", false), redis}}

	tg, err := Discover(ctx, f, nil, "db1")
	if err != nil {
		t.Fatal(err)
	}
	if tg.User != "root" || tg.Password != "p@ss:w" || tg.Database != "app" || tg.Client != "mysql" {
		t.Errorf("unexpected target: %+v", tg)
	}

	if _, err := Discover(ctx, f, nil, "db2"); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("stopped server: %v", err)
	}
	if _, err := Discover(ctx, f, nil, "cache"); err == nil || !strings.Contains(err.Error(), "not a MySQL") {
		t.Errorf("non-mysql server: %v", err)
	}
	if _, err := Discover(ctx, f, nil, "nope"); err == nil || !strings.Contains(err.Error(), "db1, db2") {
		t.Errorf("missing server should list the options: %v", err)
	}

	// No name: the only running MySQL server is used.
	if tg, err := Discover(ctx, f, nil, ""); err != nil || tg.Label != "db1" {
		t.Errorf("default server: %v %+v", err, tg)
	}
	f.servers = append(f.servers, mysqlServer("db3", true))
	if _, err := Discover(ctx, f, nil, ""); err == nil || !strings.Contains(err.Error(), "--server") {
		t.Errorf("two running servers should ask for --server: %v", err)
	}
	f.servers = []docker.Instance{redis}
	if _, err := Discover(ctx, f, nil, ""); err == nil {
		t.Error("expected an error when no MySQL server runs")
	}
}

const sampleXML = `<?xml version="1.0"?>

<resultset statement="SELECT id, name FROM users" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <row>
	<field name="id">1</field>
	<field name="name">Ann &amp; Bob</field>
  </row>
  <row>
	<field name="id">2</field>
	<field name="name" xsi:nil="true" />
  </row>
  <row>
	<field name="id">3</field>
	<field name="name"></field>
  </row>
</resultset>
`

func TestParseXML(t *testing.T) {
	res, err := parseXML([]byte(sampleXML), 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Columns, ",") != "id,name" || res.RowCount != 3 || len(res.Rows) != 3 || res.Truncated {
		t.Fatalf("unexpected result: %+v", res)
	}
	if *res.Rows[0][1] != "Ann & Bob" {
		t.Errorf("entities not decoded: %q", *res.Rows[0][1])
	}
	if res.Rows[1][1] != nil {
		t.Error("NULL should stay nil")
	}
	if res.Rows[2][1] == nil || *res.Rows[2][1] != "" {
		t.Error("empty string must not become NULL")
	}

	cut, _ := parseXML([]byte(sampleXML), 2)
	if len(cut.Rows) != 2 || cut.RowCount != 3 || !cut.Truncated || cut.Note == "" {
		t.Errorf("row limit not applied: %+v", cut)
	}

	empty, err := parseXML([]byte("<?xml version=\"1.0\"?>\n<resultset statement=\"x\">\n</resultset>\n"), 10)
	if err != nil || empty.RowCount != 0 || len(empty.Rows) != 0 {
		t.Errorf("empty result: %v %+v", err, empty)
	}
}

// rpc sends requests through a Server and returns the decoded responses.
func rpc(t *testing.T, s *Server, requests ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	var all []map[string]any
	dec := json.NewDecoder(&out)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		all = append(all, m)
	}
	return all
}

func toolCall(id int, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func resultText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	res, _ := resp["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("unexpected response: %v", resp)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func TestProtocolHandshakeAndToolList(t *testing.T) {
	s := New(&fake{}, nil, "", "test")
	got := rpc(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"nothing/here"}`,
		`not json`,
	)
	if len(got) != 5 { // the notification gets no answer
		t.Fatalf("expected 5 responses, got %d: %v", len(got), got)
	}
	if v := got[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Errorf("protocol version not echoed: %v", v)
	}
	list := got[2]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, x := range list {
		names[x.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"list_databases", "list_tables", "describe_table", "query"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	if got[3]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method should be -32601: %v", got[3])
	}
	if got[4]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Errorf("bad JSON should be -32700: %v", got[4])
	}
}

func TestQueryToolRunsInReadOnlySession(t *testing.T) {
	f := &fake{servers: []docker.Instance{mysqlServer("db1", true)}, out: docker.ExecResult{Stdout: []byte(sampleXML)}}
	s := New(f, nil, "db1", "test")

	got := rpc(t, s, toolCall(1, "query", `{"sql":"SELECT id, name FROM users","limit":2}`))
	text, isErr := resultText(t, got[0])
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var res Result
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || !res.Truncated || res.RowCount != 3 {
		t.Errorf("unexpected result: %+v", res)
	}

	script := f.cmd[len(f.cmd)-1]
	if !strings.HasPrefix(script, "SET SESSION TRANSACTION READ ONLY;") || !strings.HasSuffix(script, "SELECT id, name FROM users") {
		t.Errorf("query must run after the read-only setup: %q", script)
	}
	if f.cmd[0] != "mysql" || !contains(f.cmd, "--user=root") || !contains(f.cmd, "--database=app") {
		t.Errorf("unexpected command: %v", f.cmd)
	}
	for _, a := range f.cmd {
		if strings.Contains(a, "p@ss") {
			t.Errorf("password must not be on the command line: %q", a)
		}
	}
	if len(f.env) != 1 || f.env[0] != "MYSQL_PWD=p@ss:w" {
		t.Errorf("password should go through the environment: %v", f.env)
	}
}

func TestQueryToolRefusesWritesWithoutRunningThem(t *testing.T) {
	f := &fake{servers: []docker.Instance{mysqlServer("db1", true)}}
	s := New(f, nil, "db1", "test")
	got := rpc(t, s, toolCall(1, "query", `{"sql":"DELETE FROM users"}`))
	text, isErr := resultText(t, got[0])
	if !isErr || !strings.Contains(text, "refused") {
		t.Errorf("expected a refusal, got %q (isError=%v)", text, isErr)
	}
	if f.calls != 0 {
		t.Error("a refused query must never reach the database")
	}
}

func TestMySQLReadOnlyErrorIsExplained(t *testing.T) {
	f := &fake{
		servers: []docker.Instance{mysqlServer("db1", true)},
		out: docker.ExecResult{ExitCode: 1, Stderr: []byte(
			"mysql: [Warning] something\nERROR 1792 (25006) at line 1: Cannot execute statement in a READ ONLY transaction.\n")},
	}
	got := rpc(t, New(f, nil, "db1", "test"), toolCall(1, "query", `{"sql":"SELECT 1"}`))
	text, isErr := resultText(t, got[0])
	if !isErr || !strings.Contains(text, "READ ONLY") || !strings.Contains(text, "read-only") || strings.Contains(text, "[Warning]") {
		t.Errorf("unexpected message: %q", text)
	}
}

func TestToolErrorsAreFriendly(t *testing.T) {
	f := &fake{servers: []docker.Instance{mysqlServer("db1", false)}}
	s := New(f, nil, "db1", "test")
	got := rpc(t, s,
		toolCall(1, "list_databases", `{}`),
		toolCall(2, "describe_table", `{"table":"users; DROP TABLE x"}`),
		toolCall(3, "list_tables", `{"database":"a`+"`"+`b"}`),
		toolCall(4, "no_such_tool", `{}`),
	)
	if text, isErr := resultText(t, got[0]); !isErr || !strings.Contains(text, "stopped") {
		t.Errorf("stopped server: %q", text)
	}
	if text, isErr := resultText(t, got[1]); !isErr || !strings.Contains(text, "plain name") {
		t.Errorf("bad table name: %q", text)
	}
	if text, isErr := resultText(t, got[2]); !isErr || !strings.Contains(text, "plain name") {
		t.Errorf("bad database name: %q", text)
	}
	if got[3]["error"] == nil {
		t.Errorf("unknown tool should be a protocol error: %v", got[3])
	}
	if f.calls != 0 {
		t.Error("nothing should have reached the database")
	}
}

func TestHelperToolsBuildFixedStatements(t *testing.T) {
	f := &fake{servers: []docker.Instance{mysqlServer("db1", true)}, out: docker.ExecResult{Stdout: []byte(sampleXML)}}
	s := New(f, nil, "db1", "test")
	rpc(t, s, toolCall(1, "describe_table", `{"table":"users","database":"shop"}`))
	if !strings.HasSuffix(f.cmd[len(f.cmd)-1], "SHOW FULL COLUMNS FROM `users` FROM `shop`") {
		t.Errorf("unexpected statement: %q", f.cmd[len(f.cmd)-1])
	}
	rpc(t, s, toolCall(1, "list_tables", `{}`))
	if !strings.HasSuffix(f.cmd[len(f.cmd)-1], "SHOW FULL TABLES") {
		t.Errorf("unexpected statement: %q", f.cmd[len(f.cmd)-1])
	}
}

func TestMariaDBUsesItsOwnClientAndTimeout(t *testing.T) {
	inst := mysqlServer("m", true)
	inst.TemplateID, inst.Image = "mariadb", "mariadb:11"
	f := &fake{servers: []docker.Instance{inst}, out: docker.ExecResult{Stdout: []byte(sampleXML)}}
	rpc(t, New(f, nil, "m", "test"), toolCall(1, "query", `{"sql":"SELECT 1"}`))
	if f.cmd[0] != "mariadb" || !strings.Contains(f.cmd[len(f.cmd)-1], "max_statement_time") {
		t.Errorf("unexpected command: %v", f.cmd)
	}
}

func TestOutputTooLargeAndTimeoutMessages(t *testing.T) {
	f := &fake{servers: []docker.Instance{mysqlServer("db1", true)}, err: docker.ErrOutputTooLarge}
	got := rpc(t, New(f, nil, "db1", "test"), toolCall(1, "query", `{"sql":"SELECT * FROM big"}`))
	if text, isErr := resultText(t, got[0]); !isErr || !strings.Contains(text, "LIMIT") {
		t.Errorf("too large: %q", text)
	}
	f.err = context.DeadlineExceeded
	got = rpc(t, New(f, nil, "db1", "test"), toolCall(1, "query", `{"sql":"SELECT SLEEP(30)"}`))
	if text, isErr := resultText(t, got[0]); !isErr || !strings.Contains(text, "too long") {
		t.Errorf("timeout: %q", text)
	}
	f.err = errors.New("boom")
	got = rpc(t, New(f, nil, "db1", "test"), toolCall(1, "query", `{"sql":"SELECT 1"}`))
	if _, isErr := resultText(t, got[0]); !isErr {
		t.Error("exec failure should be a tool error")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- external databases

func foreignDB(name string, running bool) docker.Foreign {
	state, status := "exited", "Exited (0) 2 hours ago"
	if running {
		state, status = "running", "Up 5 minutes"
	}
	return docker.Foreign{
		ID: "ctr-" + name, Name: name, Image: "mysql:8.4",
		State: state, StatusText: status, Client: "mysql", Compose: "shop",
	}
}

func shopConn() external.Conn {
	return external.Conn{
		Name: "shop", Container: "shop-db-1", Client: "mysql",
		User: "reader", Password: "s3cret", Database: "shop",
	}
}

func TestDiscoverFindsSavedConnection(t *testing.T) {
	ctx := context.Background()
	f := &fake{foreign: []docker.Foreign{foreignDB("shop-db-1", true)}}
	cs := conns{"shop": shopConn()}

	tg, err := Discover(ctx, f, cs, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if tg.ContainerID != "ctr-shop-db-1" {
		t.Errorf("should exec in the container it found, got %q", tg.ContainerID)
	}
	if tg.Label != "shop" || tg.User != "reader" || tg.Password != "s3cret" || tg.Database != "shop" {
		t.Errorf("unexpected target: %+v", tg)
	}
}

// A MiniDock server and a saved connection may share a name; the server wins,
// because that is the one the person sees on the Servers tab.
func TestMiniDockServerWinsOverSavedConnection(t *testing.T) {
	ctx := context.Background()
	f := &fake{
		servers: []docker.Instance{mysqlServer("shop", true)},
		foreign: []docker.Foreign{foreignDB("shop-db-1", true)},
	}
	tg, err := Discover(ctx, f, conns{"shop": shopConn()}, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if tg.ContainerID != "id-shop" || tg.User != "root" {
		t.Errorf("the MiniDock server should be used, got %+v", tg)
	}
}

func TestDiscoverExplainsAStoppedExternalContainer(t *testing.T) {
	ctx := context.Background()
	f := &fake{foreign: []docker.Foreign{foreignDB("shop-db-1", false)}}

	_, err := Discover(ctx, f, conns{"shop": shopConn()}, "shop")
	if err == nil {
		t.Fatal("a stopped container should be an error")
	}
	for _, want := range []string{"shop-db-1", "not running", "Exited"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should mention %q: %v", want, err)
		}
	}
}

func TestDiscoverExplainsAMissingExternalContainer(t *testing.T) {
	ctx := context.Background()
	f := &fake{} // the container is gone

	_, err := Discover(ctx, f, conns{"shop": shopConn()}, "shop")
	if err == nil {
		t.Fatal("a missing container should be an error")
	}
	if !strings.Contains(err.Error(), "shop-db-1") || !strings.Contains(err.Error(), "External tab") {
		t.Errorf("message should name the container and say how to fix it: %v", err)
	}
}

// The container is looked up by name on every call, so recreating it (compose up)
// keeps the connection working even though the container id changed.
func TestConnectionFollowsARecreatedContainer(t *testing.T) {
	ctx := context.Background()
	f := &fake{foreign: []docker.Foreign{foreignDB("shop-db-1", true)}}
	cs := conns{"shop": shopConn()}

	first, err := Discover(ctx, f, cs, "shop")
	if err != nil {
		t.Fatal(err)
	}
	recreated := foreignDB("shop-db-1", true)
	recreated.ID = "ctr-brand-new"
	f.foreign = []docker.Foreign{recreated}

	second, err := Discover(ctx, f, cs, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if first.ContainerID == second.ContainerID || second.ContainerID != "ctr-brand-new" {
		t.Errorf("should follow the new container: %q then %q", first.ContainerID, second.ContainerID)
	}
}

func TestDiscoverWithoutNameCountsSavedConnections(t *testing.T) {
	ctx := context.Background()

	// Only a saved connection: it is the one.
	f := &fake{foreign: []docker.Foreign{foreignDB("shop-db-1", true)}}
	tg, err := Discover(ctx, f, conns{"shop": shopConn()}, "")
	if err != nil || tg.Label != "shop" {
		t.Fatalf("the only connection should be used: %v %+v", err, tg)
	}

	// A MiniDock server as well: now it is ambiguous and must say so.
	f.servers = []docker.Instance{mysqlServer("db1", true)}
	_, err = Discover(ctx, f, conns{"shop": shopConn()}, "")
	if err == nil || !strings.Contains(err.Error(), "--server") {
		t.Errorf("two candidates should ask for --server: %v", err)
	}
	if err != nil && (!strings.Contains(err.Error(), "shop") || !strings.Contains(err.Error(), "db1")) {
		t.Errorf("both names should be listed: %v", err)
	}

	// Nothing at all.
	empty := &fake{}
	if _, err := Discover(ctx, empty, conns{}, ""); err == nil || !strings.Contains(err.Error(), "External tab") {
		t.Errorf("an empty setup should point at the External tab: %v", err)
	}
}

func TestUnknownNameListsSavedConnectionsToo(t *testing.T) {
	ctx := context.Background()
	f := &fake{servers: []docker.Instance{mysqlServer("db1", true)}}
	_, err := Discover(ctx, f, conns{"shop": shopConn()}, "typo")
	if err == nil {
		t.Fatal("an unknown name should be an error")
	}
	if !strings.Contains(err.Error(), "db1") || !strings.Contains(err.Error(), "shop") {
		t.Errorf("both kinds should be offered: %v", err)
	}
}

// A saved connection is read-only just like a MiniDock server: the session is
// made read-only and a write is refused before it is ever sent.
func TestExternalConnectionStaysReadOnly(t *testing.T) {
	f := &fake{
		foreign: []docker.Foreign{foreignDB("shop-db-1", true)},
		out:     docker.ExecResult{Stdout: []byte(sampleXML)},
	}
	s := New(f, conns{"shop": shopConn()}, "shop", "test")

	got, isErr := resultText(t, rpc(t, s, toolCall(1, "query", `{"sql":"DELETE FROM users"}`))[0])
	if !isErr || !strings.Contains(got, "refused") {
		t.Errorf("a write should be refused: %q (isError=%v)", got, isErr)
	}
	if f.calls != 0 {
		t.Errorf("a refused write must never reach the database, ran %d command(s)", f.calls)
	}

	// A read goes through, in the container the connection points at.
	if _, isErr := resultText(t, rpc(t, s, toolCall(1, "query", `{"sql":"SELECT 1"}`))[0]); isErr {
		t.Fatal("a read should be allowed")
	}
	if f.lastExec != "ctr-shop-db-1" {
		t.Errorf("ran in %q, want the connection's container", f.lastExec)
	}
	joined := strings.Join(f.cmd, " ")
	if !strings.Contains(joined, "SET SESSION TRANSACTION READ ONLY") {
		t.Errorf("the session should be made read-only: %q", joined)
	}
	if !strings.Contains(joined, "--user=reader") {
		t.Errorf("the saved user should be used: %q", joined)
	}
	// The password goes through the environment, never the command line.
	if strings.Contains(joined, "s3cret") {
		t.Errorf("the password must not be in the command: %q", joined)
	}
	if len(f.env) == 0 || f.env[0] != "MYSQL_PWD=s3cret" {
		t.Errorf("the password should be in the environment, got %v", f.env)
	}
}

func TestProbeDescribesAWorkingConnection(t *testing.T) {
	f := &fake{
		foreign: []docker.Foreign{foreignDB("shop-db-1", true)},
		out:     docker.ExecResult{Stdout: []byte(sampleXML)},
	}
	s := New(f, conns{"shop": shopConn()}, "shop", "test")

	got, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shop", "reader", "works"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary should mention %q: %q", want, got)
		}
	}

	// A broken connection reports why, instead of claiming success.
	broken := New(&fake{}, conns{"shop": shopConn()}, "shop", "test")
	if _, err := broken.Probe(context.Background()); err == nil {
		t.Error("probing a missing container should fail")
	}
}

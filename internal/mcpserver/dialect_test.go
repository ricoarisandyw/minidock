package mcpserver

import (
	"strings"
	"testing"

	"minidock/internal/docker"
)

func TestDialectForKnowsTheThreeFamilies(t *testing.T) {
	cases := map[string]string{"mysql": "mysql", "mariadb": "mariadb", "psql": "psql"}
	for program, want := range cases {
		d, err := DialectFor(program)
		if err != nil {
			t.Fatalf("%s: %v", program, err)
		}
		if d.Program != want {
			t.Errorf("%s: program = %q, want %q", program, d.Program, want)
		}
	}
	if _, err := DialectFor("mongo"); err == nil {
		t.Error("an unknown client should be refused")
	}
}

// Each family must lock the session itself, so a write is refused by the database
// even if the statement check were bypassed.
func TestEveryDialectMakesTheSessionReadOnly(t *testing.T) {
	for _, program := range []string{"mysql", "mariadb", "psql"} {
		d, err := DialectFor(program)
		if err != nil {
			t.Fatal(err)
		}
		got := d.wrap("SELECT 1")
		if !strings.Contains(strings.ToUpper(got), "READ ONLY") {
			t.Errorf("%s does not open a read-only session: %q", program, got)
		}
		if !strings.Contains(got, "SELECT 1") {
			t.Errorf("%s lost the statement: %q", program, got)
		}
	}
}

// The password must travel in the environment, never on the command line, where
// anyone running `ps` could read it.
func TestNoDialectPutsThePasswordInTheCommand(t *testing.T) {
	for _, program := range []string{"mysql", "mariadb", "psql"} {
		d, _ := DialectFor(program)
		target := Target{Client: program, User: "reader", Password: "s3cret", Database: "shop"}
		joined := strings.Join(d.args(target), " ")
		if strings.Contains(joined, "s3cret") {
			t.Errorf("%s puts the password in the command: %q", program, joined)
		}
		if d.PasswordEnv == "" {
			t.Errorf("%s has no environment variable for the password", program)
		}
	}
}

func TestPostgresWrapRunsInsideAReadOnlyTransaction(t *testing.T) {
	d, _ := DialectFor("psql")
	got := d.wrap("SELECT * FROM users")
	for _, want := range []string{"BEGIN TRANSACTION READ ONLY", "statement_timeout", "COMMIT"} {
		if !strings.Contains(got, want) {
			t.Errorf("wrapped SQL should contain %q: %q", want, got)
		}
	}
	// A trailing semicolon must not produce an empty extra statement.
	if strings.Contains(d.wrap("SELECT 1;"), ";; ") {
		t.Errorf("a trailing semicolon was not tidied: %q", d.wrap("SELECT 1;"))
	}
}

// WITH is an ordinary read in Postgres, but the MySQL guard refuses it, and
// neither should start accepting what the other allows.
func TestAllowedStatementsDifferPerFamily(t *testing.T) {
	if err := CheckReadOnly(postgres, "WITH x AS (SELECT 1) SELECT * FROM x"); err != nil {
		t.Errorf("Postgres should allow a CTE: %v", err)
	}
	if err := CheckReadOnly(mysql, "WITH x AS (SELECT 1) SELECT * FROM x"); err == nil {
		t.Error("the MySQL guard should still refuse WITH")
	}
	if err := CheckReadOnly(postgres, "TABLE users"); err != nil {
		t.Errorf("Postgres should allow TABLE: %v", err)
	}
}

func TestPostgresGuardRefusesItsOwnRiskyThings(t *testing.T) {
	for _, q := range []string{
		"COPY users TO '/tmp/x'",
		"copy users from '/tmp/x'",
		"SELECT $$ hidden $$",
		"SELECT pg_read_file('/etc/passwd')",
		"SELECT pg_ls_dir('/')",
		"SELECT lo_export(1, '/tmp/x')",
		"SELECT nextval('seq')",
		"SELECT setval('seq', 1)",
		"EXPLAIN ANALYZE SELECT 1",
		"SELECT * FROM t FOR UPDATE",
		"SELECT 1 INTO other",
		// The shape rules still apply.
		"SELECT 1; DROP TABLE x",
		"SELECT 1 -- hi",
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET a = 1",
		"DROP TABLE t",
		"CREATE TABLE t (a int)",
		"GRANT ALL ON t TO x",
	} {
		if err := CheckReadOnly(postgres, q); err == nil {
			t.Errorf("%q should be refused", q)
		}
	}
}

func TestPostgresGuardAllowsOrdinaryReads(t *testing.T) {
	for _, q := range []string{
		"SELECT 1",
		"select * from users where name = 'Bob' limit 5",
		"SELECT count(*) FROM orders",
		"EXPLAIN SELECT * FROM users",
		"SHOW timezone",
		`SELECT 'it''s fine'`,
	} {
		if err := CheckReadOnly(postgres, q); err != nil {
			t.Errorf("%q should be allowed: %v", q, err)
		}
	}
}

// The helper tools must produce statements the guard would accept, and must not
// reach outside the table the caller named.
func TestPostgresHelperStatements(t *testing.T) {
	d, _ := DialectFor("psql")

	tables, err := d.tablesSQL("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tables, "information_schema.tables") {
		t.Errorf("list_tables should read information_schema: %q", tables)
	}
	if !strings.Contains(tables, "pg_catalog") {
		t.Errorf("system schemas should be filtered out: %q", tables)
	}

	cols, err := d.columnsSQL("", "users")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cols, "'users'") {
		t.Errorf("describe_table should quote the table name: %q", cols)
	}
	// A quote in the name must not break out of the literal.
	quoted := pgQuoteLiteral("o'brien")
	if quoted != "'o''brien'" {
		t.Errorf("pgQuoteLiteral(%q) = %q", "o'brien", quoted)
	}
}

// ---------------------------------------------------------------- CSV parsing

func TestParseCSVReadsOrdinaryRows(t *testing.T) {
	in := "id,name\n1,Alice\n2,Bob\n"
	res, err := parseCSV([]byte(in), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 2 || res.Columns[0] != "id" || res.Columns[1] != "name" {
		t.Errorf("columns = %v", res.Columns)
	}
	if res.RowCount != 2 || len(res.Rows) != 2 {
		t.Fatalf("rows = %d/%d", len(res.Rows), res.RowCount)
	}
	if *res.Rows[0][1] != "Alice" || *res.Rows[1][1] != "Bob" {
		t.Errorf("values = %v", res.Rows)
	}
}

// The whole reason for the NULL marker: an empty string and NULL must not look
// the same to whoever reads the answer.
func TestParseCSVTellsNullFromEmptyString(t *testing.T) {
	in := "a,b\n" + pgNullMarker + ",\n"
	res, err := parseCSV([]byte(in), 10)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]
	if row[0] != nil {
		t.Errorf("first value should be NULL, got %q", *row[0])
	}
	if row[1] == nil || *row[1] != "" {
		t.Errorf("second value should be the empty string, got %v", row[1])
	}
}

func TestParseCSVHandlesCommasQuotesAndNewlines(t *testing.T) {
	in := "note\n" + `"a, b"` + "\n" + `"say ""hi"""` + "\n" + `"line1
line2"` + "\n"
	res, err := parseCSV([]byte(in), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a, b", `say "hi"`, "line1\nline2"}
	if len(res.Rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(res.Rows), len(want), res.Rows)
	}
	for i, w := range want {
		if got := *res.Rows[i][0]; got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
}

func TestParseCSVRespectsTheRowLimit(t *testing.T) {
	in := "id\n1\n2\n3\n4\n5\n"
	res, err := parseCSV([]byte(in), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Errorf("kept %d rows, want 2", len(res.Rows))
	}
	if res.RowCount != 5 {
		t.Errorf("RowCount = %d, want 5", res.RowCount)
	}
	if !res.Truncated || res.Note == "" {
		t.Errorf("a cut result should say so: %+v", res)
	}
}

func TestParseCSVHandlesEmptyOutput(t *testing.T) {
	for _, in := range []string{"", "   \n"} {
		res, err := parseCSV([]byte(in), 10)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if len(res.Rows) != 0 || res.RowCount != 0 {
			t.Errorf("%q should give nothing, got %+v", in, res)
		}
	}
}

// A statement that returns no rows at all still has a header, and must not be
// reported as an error.
func TestParseCSVHandlesHeaderOnly(t *testing.T) {
	res, err := parseCSV([]byte("id,name\n"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 2 || res.RowCount != 0 {
		t.Errorf("header-only result = %+v", res)
	}
}

// ---------------------------------------------------------------- Postgres end to end

func postgresServer(name string, running bool) docker.Instance {
	state := "exited"
	if running {
		state = "running"
	}
	return docker.Instance{
		ID: "id-" + name, Name: name, TemplateID: "postgres", Image: "postgres:17", State: state,
		Connection: "postgresql://postgres:pgpass@localhost:5432/app",
	}
}

// The whole path for a Postgres server: discovery, the psql command line, the
// read-only transaction, and CSV coming back as rows.
func TestPostgresServerQueryEndToEnd(t *testing.T) {
	f := &fake{
		servers: []docker.Instance{postgresServer("pg", true)},
		out:     docker.ExecResult{Stdout: []byte("id,name\n1,Alice\n")},
	}
	s := New(f, nil, "pg", "test")

	text, isErr := resultText(t, rpc(t, s, toolCall(1, "query", `{"sql":"SELECT id, name FROM users"}`))[0])
	if isErr {
		t.Fatalf("a read should work: %s", text)
	}
	if !strings.Contains(text, "Alice") {
		t.Errorf("rows did not come back: %s", text)
	}

	joined := strings.Join(f.cmd, " ")
	if f.cmd[0] != "psql" {
		t.Errorf("should run psql, got %q", f.cmd[0])
	}
	for _, want := range []string{"--csv", "--username=postgres", "BEGIN TRANSACTION READ ONLY"} {
		if !strings.Contains(joined, want) {
			t.Errorf("command should contain %q: %q", want, joined)
		}
	}
	if strings.Contains(joined, "pgpass") {
		t.Errorf("the password must not be in the command: %q", joined)
	}
	if len(f.env) == 0 || f.env[0] != "PGPASSWORD=pgpass" {
		t.Errorf("the password should be in the environment, got %v", f.env)
	}
}

func TestPostgresWriteIsRefusedBeforeItReachesTheDatabase(t *testing.T) {
	f := &fake{servers: []docker.Instance{postgresServer("pg", true)}}
	s := New(f, nil, "pg", "test")

	for _, sql := range []string{"DELETE FROM users", "UPDATE users SET a = 1", "COPY users TO '/tmp/x'"} {
		text, isErr := resultText(t, rpc(t, s, toolCall(1, "query", `{"sql":"`+sql+`"}`))[0])
		if !isErr || !strings.Contains(text, "refused") {
			t.Errorf("%q should be refused, got %q", sql, text)
		}
	}
	if f.calls != 0 {
		t.Errorf("a refused write must never reach the database, ran %d command(s)", f.calls)
	}
}

// The helper tools must send Postgres SQL, not the MySQL SHOW statements.
func TestPostgresHelperToolsSendPostgresSQL(t *testing.T) {
	f := &fake{
		servers: []docker.Instance{postgresServer("pg", true)},
		out:     docker.ExecResult{Stdout: []byte("Name\nusers\n")},
	}
	s := New(f, nil, "pg", "test")

	rpc(t, s, toolCall(1, "list_tables", `{}`))
	if joined := strings.Join(f.cmd, " "); !strings.Contains(joined, "information_schema.tables") {
		t.Errorf("list_tables sent the wrong SQL: %q", joined)
	}

	rpc(t, s, toolCall(1, "describe_table", `{"table":"users"}`))
	joined := strings.Join(f.cmd, " ")
	if !strings.Contains(joined, "information_schema.columns") || !strings.Contains(joined, "'users'") {
		t.Errorf("describe_table sent the wrong SQL: %q", joined)
	}

	rpc(t, s, toolCall(1, "list_databases", `{}`))
	if joined := strings.Join(f.cmd, " "); !strings.Contains(joined, "pg_database") {
		t.Errorf("list_databases sent the wrong SQL: %q", joined)
	}
}

// A saved connection to someone else's Postgres container works the same way.
func TestExternalPostgresConnection(t *testing.T) {
	f := &fake{
		foreign: []docker.Foreign{{
			ID: "ctr-pg", Name: "app-db-1", Image: "postgres:16", State: "running",
			StatusText: "Up 3 minutes", Client: "psql", Compose: "app",
		}},
		out: docker.ExecResult{Stdout: []byte("Name\norders\n")},
	}
	cs := conns{"app": {
		Name: "app", Container: "app-db-1", Client: "psql",
		User: "reader", Password: "pw", Database: "app",
	}}
	s := New(f, cs, "app", "test")

	text, isErr := resultText(t, rpc(t, s, toolCall(1, "list_tables", `{}`))[0])
	if isErr {
		t.Fatalf("listing tables should work: %s", text)
	}
	if f.lastExec != "ctr-pg" {
		t.Errorf("ran in %q, want the connection's container", f.lastExec)
	}
	if f.cmd[0] != "psql" {
		t.Errorf("should run psql, got %q", f.cmd[0])
	}
}

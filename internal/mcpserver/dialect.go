package mcpserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Dialect is everything that differs between database families. Every other part
// of the MCP server asks the dialect instead of checking which database it is
// talking to, so adding one more means adding one more value here.
type Dialect struct {
	// Name is what the person sees in messages: "MySQL", "MariaDB", "PostgreSQL".
	Name string
	// Program is the client inside the container: "mysql", "mariadb" or "psql".
	Program string
	// PasswordEnv carries the password to the client without putting it on the
	// command line, where it would show up in the process list.
	PasswordEnv string
	// allowedFirstWords are the statements the guard lets through.
	allowedFirstWords map[string]bool
	// args builds the command line for one query, minus the SQL itself.
	args func(t Target) []string
	// sqlFlag is the option that carries the statement: "-e" for mysql, "-c" for psql.
	sqlFlag string
	// wrap turns the checked SQL into what is actually sent, including whatever
	// makes the session read-only.
	wrap func(sql string) string
	// parse reads the client's output into a table.
	parse func(out []byte, limit int) (Result, error)
	// checkExtra holds the refusals that only make sense for this family.
	checkExtra func(words []string, sql string) error
	// tablesSQL and columnsSQL answer the list_tables and describe_table tools.
	tablesSQL  func(database string) (string, error)
	columnsSQL func(database, table string) (string, error)
}

// DialectFor returns the dialect for a client program name.
func DialectFor(program string) (Dialect, error) {
	switch program {
	case "mysql":
		return mysqlDialect("MySQL", "mysql"), nil
	case "mariadb":
		return mysqlDialect("MariaDB", "mariadb"), nil
	case "psql":
		return postgresDialect(), nil
	}
	return Dialect{}, fmt.Errorf("unsupported database client %q", program)
}

// mysqlDialect covers MySQL and MariaDB, which differ only in the program name
// and in how the statement timeout is spelled.
func mysqlDialect(name, program string) Dialect {
	return Dialect{
		Name:        name,
		Program:     program,
		PasswordEnv: "MYSQL_PWD",
		sqlFlag:     "-e",
		allowedFirstWords: map[string]bool{
			"select": true, "show": true, "describe": true, "desc": true, "explain": true,
		},
		args: func(t Target) []string {
			cmd := []string{
				t.Client, "--xml", "--connect-timeout=5", "--default-character-set=utf8mb4",
				"--user=" + t.User,
			}
			if t.Database != "" {
				cmd = append(cmd, "--database="+t.Database)
			}
			return cmd
		},
		wrap: func(sql string) string {
			// A read-only session makes MySQL itself refuse writes (error 1792),
			// whatever the statement text looks like.
			timeout := "SET SESSION MAX_EXECUTION_TIME=10000; "
			if program == "mariadb" {
				timeout = "SET SESSION max_statement_time=10; "
			}
			return "SET SESSION TRANSACTION READ ONLY; " + timeout + sql
		},
		parse:      parseXML,
		checkExtra: mysqlExtraChecks,
		tablesSQL: func(database string) (string, error) {
			from, err := fromClause(database)
			if err != nil {
				return "", err
			}
			return "SHOW FULL TABLES" + from, nil
		},
		columnsSQL: func(database, table string) (string, error) {
			from, err := fromClause(database)
			if err != nil {
				return "", err
			}
			return "SHOW FULL COLUMNS FROM `" + table + "`" + from, nil
		},
	}
}

// postgresDialect talks to psql. Results come back as CSV: it leaves the person's
// SQL untouched, where wrapping each query in row_to_json would rewrite it.
func postgresDialect() Dialect {
	return Dialect{
		Name:        "PostgreSQL",
		Program:     "psql",
		PasswordEnv: "PGPASSWORD",
		sqlFlag:     "-c",
		allowedFirstWords: map[string]bool{
			// WITH is a plain read in Postgres, as long as it holds no data-modifying
			// statement — which the word scan below refuses anyway.
			"select": true, "show": true, "explain": true, "table": true, "values": true, "with": true,
		},
		args: func(t Target) []string {
			cmd := []string{
				t.Client, "--no-password", "--no-psqlrc", "--quiet",
				"--username=" + t.User,
				// CSV with a chosen NULL marker, so NULL is not the empty string.
				"--csv", "--pset=null=" + pgNullMarker,
				"--variable=ON_ERROR_STOP=1",
			}
			if t.Database != "" {
				cmd = append(cmd, "--dbname="+t.Database)
			}
			return cmd
		},
		wrap: func(sql string) string {
			// The transaction is what actually stops writes: Postgres refuses them
			// with error 25006 even if the statement check were bypassed.
			return "SET statement_timeout = '" + strconv.Itoa(int(queryTimeout.Seconds())) + "s'; " +
				"BEGIN TRANSACTION READ ONLY; " + ensureSemicolon(sql) + " COMMIT;"
		},
		parse:      parseCSV,
		checkExtra: postgresExtraChecks,
		tablesSQL: func(database string) (string, error) {
			// database is ignored: psql connects to one database at a time, and the
			// connection already chose it.
			return `SELECT table_schema AS "Schema", table_name AS "Name", table_type AS "Type" ` +
				`FROM information_schema.tables ` +
				`WHERE table_schema NOT IN ('pg_catalog', 'information_schema') ` +
				`ORDER BY table_schema, table_name`, nil
		},
		columnsSQL: func(database, table string) (string, error) {
			return `SELECT column_name AS "Field", data_type AS "Type", is_nullable AS "Null", ` +
				`column_default AS "Default" FROM information_schema.columns ` +
				`WHERE table_name = ` + pgQuoteLiteral(table) + ` ` +
				`ORDER BY ordinal_position`, nil
		},
	}
}

// pgNullMarker tells NULL apart from the empty string in CSV output. It is a
// control character, so it cannot be confused with a value a table really holds.
const pgNullMarker = "\x00NULL\x00"

// pgQuoteLiteral wraps a value in single quotes for Postgres, doubling any quote
// inside it. Only used for names the tools already checked against identRe.
func pgQuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ensureSemicolon adds the semicolon that separates the query from the COMMIT
// that follows it. The guard allows at most one, so adding one is safe.
func ensureSemicolon(sql string) string {
	s := strings.TrimRight(strings.TrimSpace(sql), ";")
	return s + ";"
}

// mysqlExtraChecks are the refusals that apply to MySQL and MariaDB.
func mysqlExtraChecks(words []string, _ string) error {
	for i, w := range words {
		switch w {
		case "into":
			return errors.New("INTO is not allowed (it can write files or variables)")
		case "load_file":
			return errors.New("LOAD_FILE is not allowed")
		case "analyze":
			return errors.New("EXPLAIN ANALYZE is not allowed because it runs the query")
		case "for":
			if i+1 < len(words) && (words[i+1] == "update" || words[i+1] == "share") {
				return errors.New("locking reads (FOR UPDATE / FOR SHARE) are not allowed")
			}
		case "lock":
			if i+1 < len(words) && words[i+1] == "in" {
				return errors.New("locking reads (LOCK IN SHARE MODE) are not allowed")
			}
		}
	}
	return nil
}

// postgresExtraChecks refuses what is risky in Postgres specifically: dollar
// quoting (which hides whole function bodies from the scan), COPY (reads and
// writes files), and the server-side file functions.
func postgresExtraChecks(words []string, sql string) error {
	if strings.Contains(sql, "$$") {
		return errors.New("dollar quoting ($$) is not allowed")
	}
	for i, w := range words {
		switch w {
		case "copy":
			return errors.New("COPY is not allowed (it reads and writes files)")
		case "analyze":
			return errors.New("EXPLAIN ANALYZE is not allowed because it runs the query")
		case "into":
			return errors.New("INTO is not allowed (SELECT INTO creates a table)")
		case "pg_read_file", "pg_read_binary_file", "pg_ls_dir", "pg_stat_file", "lo_import", "lo_export":
			return fmt.Errorf("%s is not allowed", strings.ToUpper(w))
		case "for":
			if i+1 < len(words) && (words[i+1] == "update" || words[i+1] == "share") {
				return errors.New("locking reads (FOR UPDATE / FOR SHARE) are not allowed")
			}
		case "nextval", "setval":
			return fmt.Errorf("%s is not allowed because it changes a sequence", strings.ToUpper(w))
		}
	}
	return nil
}

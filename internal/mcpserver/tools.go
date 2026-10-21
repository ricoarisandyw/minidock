package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// tool is one thing the AI can call.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
	run         func(ctx context.Context, s *Server, args json.RawMessage) (Result, error)
}

var identRe = regexp.MustCompile(`^[A-Za-z0-9_$-]{1,64}$`)

func objectSchema(required []string, props map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

var strProp = func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func tools() []tool {
	readOnly := map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
	return []tool{
		{
			Name:        "list_databases",
			Description: "List the databases on the server.",
			InputSchema: objectSchema(nil, map[string]any{}),
			Annotations: readOnly,
			run: func(ctx context.Context, s *Server, _ json.RawMessage) (Result, error) {
				return s.execDialect(ctx, func(d Dialect) (string, error) {
					if d.Program == "psql" {
						return `SELECT datname AS "Database" FROM pg_database ` +
							`WHERE datallowconn ORDER BY datname`, nil
					}
					return "SHOW DATABASES", nil
				}, DefaultRowLimit)
			},
		},
		{
			Name:        "list_tables",
			Description: "List the tables and views of a database. Uses the server's default database when none is given.",
			InputSchema: objectSchema(nil, map[string]any{"database": strProp("Database name (optional)")}),
			Annotations: readOnly,
			run: func(ctx context.Context, s *Server, args json.RawMessage) (Result, error) {
				var in struct {
					Database string `json:"database"`
				}
				if err := decodeArgs(args, &in); err != nil {
					return Result{}, err
				}
				// Checked here, not in the dialect, so a bad name is reported even
				// when the server turns out to be stopped or missing.
				if err := checkIdent("database", in.Database); err != nil {
					return Result{}, err
				}
				return s.execDialect(ctx, func(d Dialect) (string, error) {
					return d.tablesSQL(in.Database)
				}, MaxRowLimit)
			},
		},
		{
			Name:        "describe_table",
			Description: "Show the columns of a table: name, type, nullability, key, default and comment.",
			InputSchema: objectSchema([]string{"table"}, map[string]any{
				"table":    strProp("Table name"),
				"database": strProp("Database name (optional)"),
			}),
			Annotations: readOnly,
			run: func(ctx context.Context, s *Server, args json.RawMessage) (Result, error) {
				var in struct {
					Table    string `json:"table"`
					Database string `json:"database"`
				}
				if err := decodeArgs(args, &in); err != nil {
					return Result{}, err
				}
				if !identRe.MatchString(in.Table) {
					return Result{}, errors.New("table must be a plain name (letters, digits, _ $ -)")
				}
				if err := checkIdent("database", in.Database); err != nil {
					return Result{}, err
				}
				return s.execDialect(ctx, func(d Dialect) (string, error) {
					return d.columnsSQL(in.Database, in.Table)
				}, MaxRowLimit)
			},
		},
		{
			Name: "query",
			Description: "Run one read-only SQL statement (SELECT, SHOW, EXPLAIN and the like). " +
				"Anything that could change data is refused. Results are limited to a number of rows and a 10 second runtime.",
			InputSchema: objectSchema([]string{"sql"}, map[string]any{
				"sql": strProp("A single read-only statement, such as SELECT, SHOW or EXPLAIN"),
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum rows to return (default %d, max %d)", DefaultRowLimit, MaxRowLimit),
					"minimum":     1,
					"maximum":     MaxRowLimit,
				},
			}),
			Annotations: readOnly,
			run: func(ctx context.Context, s *Server, args json.RawMessage) (Result, error) {
				var in struct {
					SQL   string `json:"sql"`
					Limit int    `json:"limit"`
				}
				if err := decodeArgs(args, &in); err != nil {
					return Result{}, err
				}
				limit := in.Limit
				if limit <= 0 {
					limit = DefaultRowLimit
				}
				if limit > MaxRowLimit {
					limit = MaxRowLimit
				}
				// The shape of the statement does not depend on the database, so it
				// is judged before discovery: a refusal reads as a refusal even when
				// the server is stopped, and nothing reaches Docker either way.
				if err := CheckShape(in.SQL); err != nil {
					return Result{}, fmt.Errorf("refused: %w", err)
				}
				return s.execDialect(ctx, func(d Dialect) (string, error) {
					if err := CheckReadOnly(d, in.SQL); err != nil {
						return "", fmt.Errorf("refused: %w", err)
					}
					return strings.TrimSpace(in.SQL), nil
				}, limit)
			},
		},
	}
}

func decodeArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("the arguments are not valid: %w", err)
	}
	return nil
}

// checkIdent refuses a name that is not a plain identifier. An empty name means
// "use the default", which is always fine.
func checkIdent(what, name string) error {
	if name == "" || identRe.MatchString(name) {
		return nil
	}
	return errors.New(what + " must be a plain name (letters, digits, _ $ -)")
}

// fromClause builds " FROM `db`" for SHOW statements, or "" for the default database.
func fromClause(db string) (string, error) {
	if db == "" {
		return "", nil
	}
	if !identRe.MatchString(db) {
		return "", errors.New("database must be a plain name (letters, digits, _ $ -)")
	}
	return " FROM `" + db + "`", nil
}

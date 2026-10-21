package mcpserver

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"minidock/internal/docker"
)

// Limits that keep one query from hurting the database or flooding the AI.
const (
	DefaultRowLimit = 200
	MaxRowLimit     = 1000
	queryTimeout    = 10 * time.Second
	maxOutputBytes  = 4 << 20 // 4 MiB of raw client output
)

// Runner runs a command inside a container. *docker.Client implements it.
type Runner interface {
	Exec(ctx context.Context, id string, cmd, env []string, maxOutput int) (docker.ExecResult, error)
}

// Result is a table of text values; a nil value is SQL NULL.
type Result struct {
	Columns   []string    `json:"columns"`
	Rows      [][]*string `json:"rows"`
	RowCount  int         `json:"rowCount"`
	Truncated bool        `json:"truncated"`
	Note      string      `json:"note,omitempty"`
}

// runSQL runs one statement in a read-only session and returns up to limit rows.
// The caller is responsible for checking user-written SQL with CheckReadOnly first.
func runSQL(ctx context.Context, r Runner, t Target, sql string, limit int) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout+5*time.Second)
	defer cancel()

	d, err := DialectFor(t.Client)
	if err != nil {
		return Result{}, err
	}

	cmd := append(d.args(t), d.sqlFlag, d.wrap(sql))
	// The password goes in the environment, so it is not visible in the process list.
	env := []string{d.PasswordEnv + "=" + t.Password}

	res, err := r.Exec(ctx, t.ContainerID, cmd, env, maxOutputBytes)
	switch {
	case errors.Is(err, docker.ErrOutputTooLarge):
		return Result{}, errors.New("the result is too large. Add a LIMIT or select fewer columns")
	case errors.Is(err, context.DeadlineExceeded):
		return Result{}, errors.New("the query took too long and was stopped")
	case err != nil:
		return Result{}, fmt.Errorf("cannot run the query: %s", docker.Explain(err))
	}
	if res.ExitCode != 0 {
		return Result{}, errors.New(clientError(res.Stderr, d))
	}
	return d.parse(res.Stdout, limit)
}

// clientError turns the SQL client's stderr into one short message.
func clientError(stderr []byte, d Dialect) string {
	var keep []string
	for _, line := range strings.Split(string(stderr), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, d.Program+": [Warning]") {
			continue
		}
		keep = append(keep, line)
	}
	if len(keep) == 0 {
		return "the SQL client failed without a message"
	}
	msg := strings.Join(keep, " ")
	// MySQL says 1792, Postgres says 25006; both mean the same thing here.
	if strings.Contains(msg, "1792") || strings.Contains(msg, "25006") ||
		strings.Contains(msg, "read-only transaction") {
		msg += " (this server is read-only)"
	}
	if strings.Contains(msg, "executable file not found") {
		msg += " (the container has no " + d.Program + " program)"
	}
	return msg
}

// parseXML reads the output of `mysql --xml`. It keeps the first limit rows.
func parseXML(data []byte, limit int) (Result, error) {
	res := Result{Columns: []string{}, Rows: [][]*string{}}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false

	var row []*string
	var fieldName string
	var text strings.Builder
	var isNull, inRow, inField bool
	haveColumns := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("cannot read the SQL client output: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				inRow, row = true, nil
			case "field":
				inField, isNull = true, false
				fieldName = ""
				text.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "name":
						fieldName = a.Value
					case "nil":
						isNull = a.Value == "true"
					}
				}
				if inRow && !haveColumns {
					res.Columns = append(res.Columns, fieldName)
				}
			}
		case xml.CharData:
			if inField {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "field":
				if inRow {
					if isNull {
						row = append(row, nil)
					} else {
						v := text.String()
						row = append(row, &v)
					}
				}
				inField = false
			case "row":
				haveColumns = true
				res.RowCount++
				if len(res.Rows) < limit {
					res.Rows = append(res.Rows, row)
				} else {
					res.Truncated = true
				}
				inRow = false
			}
		}
	}
	if res.Truncated {
		res.Note = fmt.Sprintf("Showing the first %d rows of %d. Add a WHERE or LIMIT to narrow it down.", len(res.Rows), res.RowCount)
	}
	return res, nil
}

// parseCSV reads the output of `psql --csv`. The first line is the header, and a
// value equal to pgNullMarker is SQL NULL rather than the empty string.
//
// psql prints one CSV block per statement that returns rows. The wrapper adds
// SET, BEGIN and COMMIT, which return nothing, so in practice there is one block.
func parseCSV(data []byte, limit int) (Result, error) {
	res := Result{Columns: []string{}, Rows: [][]*string{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return res, nil
	}

	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // a ragged block would otherwise fail the whole read
	r.LazyQuotes = true

	header, err := r.Read()
	if err == io.EOF {
		return res, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("cannot read the SQL client output: %w", err)
	}
	res.Columns = header

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, fmt.Errorf("cannot read the SQL client output: %w", err)
		}
		res.RowCount++
		if len(res.Rows) >= limit {
			res.Truncated = true
			continue
		}
		row := make([]*string, len(rec))
		for i, v := range rec {
			if v == pgNullMarker {
				row[i] = nil // SQL NULL, not the empty string
				continue
			}
			val := v
			row[i] = &val
		}
		res.Rows = append(res.Rows, row)
	}

	if res.Truncated {
		res.Note = fmt.Sprintf("Showing the first %d rows of %d. Add a WHERE or LIMIT to narrow it down.", len(res.Rows), res.RowCount)
	}
	return res, nil
}

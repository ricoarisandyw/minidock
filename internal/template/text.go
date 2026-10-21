package template

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The functions below convert template parts to and from plain text,
// so the in-app editor can show them in simple text boxes.

// ParseVersions splits "17, 16, 15" into a list.
func ParseVersions(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// VersionsText is the inverse of ParseVersions.
func VersionsText(v []string) string { return strings.Join(v, ", ") }

// ParseEnv reads lines like KEY=VALUE. Blank lines and lines starting with # are ignored.
func ParseEnv(s string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("env line %d: expected KEY=VALUE", i+1)
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// EnvText is the inverse of ParseEnv (sorted by key).
func EnvText(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+env[k])
	}
	return strings.Join(lines, "\n")
}

// FieldsHelp explains the one-line field format shown in the editor.
const FieldsHelp = "one per line: key | label | type | default | required(yes/no) | options (a,b) | minLength"

// ParseFields reads one field per line: key | label | type | default | required | options | minLength.
// Only key is mandatory; type defaults to text and label to the key.
func ParseFields(s string) ([]Field, error) {
	var out []Field
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		for j := range parts {
			parts[j] = strings.TrimSpace(parts[j])
		}
		get := func(n int) string {
			if n < len(parts) {
				return parts[n]
			}
			return ""
		}
		f := Field{Key: get(0), Label: get(1), Type: FieldType(get(2)), Default: get(3)}
		if f.Label == "" {
			f.Label = f.Key
		}
		if f.Type == "" {
			f.Type = FieldText
		}
		switch strings.ToLower(get(4)) {
		case "yes", "y", "true", "required":
			f.Required = true
		case "", "no", "n", "false":
		default:
			return nil, fmt.Errorf("field line %d: required must be yes or no", i+1)
		}
		if opts := get(5); opts != "" {
			f.Options = ParseVersions(opts)
		}
		if m := get(6); m != "" {
			n, err := strconv.Atoi(m)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("field line %d: minLength must be a number", i+1)
			}
			f.MinLength = n
		}
		out = append(out, f)
	}
	return out, nil
}

// FieldsText is the inverse of ParseFields.
func FieldsText(fields []Field) string {
	lines := make([]string, 0, len(fields))
	for _, f := range fields {
		req := "no"
		if f.Required {
			req = "yes"
		}
		min := ""
		if f.MinLength > 0 {
			min = strconv.Itoa(f.MinLength)
		}
		lines = append(lines, strings.Join([]string{
			f.Key, f.Label, string(f.Type), f.Default, req, strings.Join(f.Options, ","), min,
		}, " | "))
	}
	return strings.Join(lines, "\n")
}

// SplitCommand splits a command line into arguments, honouring "double quotes".
func SplitCommand(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, started := false, false
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			started = true
		case (r == ' ' || r == '\t') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}

// JoinCommand is the inverse of SplitCommand.
func JoinCommand(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, "") + `"`
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

package template

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// RunSpec is everything needed to create one container.
type RunSpec struct {
	Name          string
	TemplateID    string
	Version       string
	Image         string
	Env           []string
	Cmd           []string
	ShownEnv      []string // Env with passwords hidden, for previews
	ShownCmd      []string // Cmd with passwords hidden, for previews
	HostPort      int
	ContainerPort int
	Volume        string
	DataPath      string
	Connection    string
}

// Render turns a template plus the user's answers into a RunSpec.
// Missing optional answers fall back to the field default.
func Render(t Template, v Values) (RunSpec, error) {
	vals := withDefaults(t, v)

	if err := ValidateInstanceName(vals["name"], nil); err != nil {
		return RunSpec{}, err
	}
	port, err := ValidatePort(vals["port"])
	if err != nil {
		return RunSpec{}, err
	}
	for _, f := range t.Fields {
		if err := ValidateFieldValue(f, vals[f.Key]); err != nil {
			return RunSpec{}, err
		}
	}

	vars := map[string]string{}
	for k, val := range vals {
		vars[k] = val
	}
	vars["host"] = "localhost"

	spec := RunSpec{
		Name:          vals["name"],
		TemplateID:    t.ID,
		Version:       vals["version"],
		Image:         t.Image,
		HostPort:      port,
		ContainerPort: t.ContainerPort,
		DataPath:      t.DataPath,
		Connection:    ConnectionString(t, vals),
	}
	if spec.Version != "" {
		spec.Image = t.Image + ":" + spec.Version
	}
	if t.DataPath != "" {
		spec.Volume = "minidock-" + spec.Name + "-data"
	}

	// A second copy of the variables with passwords hidden, used for previews.
	shown := map[string]string{}
	for k, val := range vars {
		shown[k] = val
	}
	for _, f := range t.Fields {
		if f.Type == FieldPassword && shown[f.Key] != "" {
			shown[f.Key] = "****"
		}
	}

	keys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		spec.Env = append(spec.Env, k+"="+expand(t.Env[k], vars, nil))
		spec.ShownEnv = append(spec.ShownEnv, k+"="+expand(t.Env[k], shown, nil))
	}
	for _, c := range t.Command {
		spec.Cmd = append(spec.Cmd, expand(c, vars, nil))
		spec.ShownCmd = append(spec.ShownCmd, expand(c, shown, nil))
	}
	return spec, nil
}

// Preview shows the equivalent `docker run` command. Passwords are hidden.
func (s RunSpec) Preview() string {
	lines := []string{
		"docker run -d",
		"  --name " + shellQuote(s.Name),
		"  --restart unless-stopped",
		fmt.Sprintf("  -p 127.0.0.1:%d:%d", s.HostPort, s.ContainerPort),
	}
	if s.Volume != "" {
		lines = append(lines, "  -v "+shellQuote(s.Volume+":"+s.DataPath))
	}
	for _, e := range s.ShownEnv {
		lines = append(lines, "  -e "+shellQuote(e))
	}
	image := "  " + shellQuote(s.Image)
	for _, c := range s.ShownCmd {
		image += " " + shellQuote(c)
	}
	lines = append(lines, image)
	return strings.Join(lines, " \\\n")
}

// ConnectionString fills the template's connection string; values are URL-escaped.
func ConnectionString(t Template, v Values) string {
	if t.ConnectionString == "" {
		return ""
	}
	vals := withDefaults(t, v)
	vars := map[string]string{"host": "localhost"}
	for k, val := range vals {
		vars[k] = val
	}
	return expand(t.ConnectionString, vars, escapeURL)
}

func withDefaults(t Template, v Values) Values {
	out := Values{}
	for k, val := range v {
		out[k] = val
	}
	if out["version"] == "" {
		out["version"] = t.DefaultVersion()
	}
	if out["port"] == "" {
		out["port"] = fmt.Sprint(t.HostPort())
	}
	for _, f := range t.Fields {
		if out[f.Key] == "" {
			out[f.Key] = f.Default
		}
	}
	return out
}

func expand(s string, vars map[string]string, escape func(string) string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		key := placeholderRe.FindStringSubmatch(m)[1]
		val, ok := vars[key]
		if !ok {
			return m
		}
		if escape != nil {
			return escape(val)
		}
		return val
	})
}

func escapeURL(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

var safeShellRe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if safeShellRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

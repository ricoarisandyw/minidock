// Package template describes server templates (image, ports, form fields)
// and turns a filled-in form into a runnable container spec.
package template

// FieldType is the kind of input a template field shows in the wizard.
type FieldType string

const (
	FieldText     FieldType = "text"
	FieldNumber   FieldType = "number"
	FieldPassword FieldType = "password"
	FieldSelect   FieldType = "select"
)

// Reserved keys are always provided by the wizard, so fields cannot reuse them.
var reservedKeys = map[string]bool{"name": true, "version": true, "port": true, "host": true}

// Field is one extra question the wizard asks (user, password, database...).
type Field struct {
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	Type      FieldType `json:"type"`
	Default   string    `json:"default,omitempty"`
	Required  bool      `json:"required,omitempty"`
	Options   []string  `json:"options,omitempty"`
	MinLength int       `json:"minLength,omitempty"`
	Help      string    `json:"help,omitempty"`
}

// Template is a recipe for one kind of server. Env, Command and
// ConnectionString may contain {{key}} placeholders.
type Template struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Icon             string            `json:"icon,omitempty"`
	Description      string            `json:"description,omitempty"`
	Image            string            `json:"image"`
	Versions         []string          `json:"versions,omitempty"`
	ContainerPort    int               `json:"containerPort"`
	DefaultHostPort  int               `json:"defaultHostPort,omitempty"`
	DataPath         string            `json:"dataPath,omitempty"`
	Fields           []Field           `json:"fields,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Command          []string          `json:"command,omitempty"`
	ConnectionString string            `json:"connectionString,omitempty"`

	// Builtin is set by the store; it is never written to disk.
	Builtin bool `json:"-"`
}

// DefaultVersion is the first listed version, or "" (meaning the image default).
func (t Template) DefaultVersion() string {
	if len(t.Versions) > 0 {
		return t.Versions[0]
	}
	return ""
}

// HostPort is the port suggested on the host.
func (t Template) HostPort() int {
	if t.DefaultHostPort > 0 {
		return t.DefaultHostPort
	}
	return t.ContainerPort
}

// Title is "icon name", used in lists and headings.
func (t Template) Title() string {
	if t.Icon == "" {
		return t.Name
	}
	return t.Icon + " " + t.Name
}

// Values holds what the user typed: name, version, port and every field key.
type Values map[string]string

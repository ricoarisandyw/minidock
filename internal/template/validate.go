package template

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

var (
	idRe          = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	keyRe         = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
	nameRe        = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	placeholderRe = regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}`)
)

// ValidateID checks a template id (lowercase, digits, - and _).
func ValidateID(id string) error {
	if !idRe.MatchString(id) {
		return errors.New("id must be lowercase letters, digits, '-' or '_' (e.g. my-redis)")
	}
	return nil
}

// ValidateTemplate reports every problem it finds, joined into one error.
func ValidateTemplate(t Template) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if err := ValidateID(t.ID); err != nil {
		add("%v", err)
	}
	if strings.TrimSpace(t.Name) == "" {
		add("name is required")
	}
	if strings.TrimSpace(t.Image) == "" {
		add("image is required")
	}
	if t.ContainerPort < 1 || t.ContainerPort > 65535 {
		add("containerPort must be between 1 and 65535")
	}
	if t.DefaultHostPort < 0 || t.DefaultHostPort > 65535 {
		add("defaultHostPort must be between 1 and 65535")
	}

	known := map[string]bool{}
	for k := range reservedKeys {
		known[k] = true
	}
	seen := map[string]bool{}
	for _, f := range t.Fields {
		switch {
		case !keyRe.MatchString(f.Key):
			add("field key %q is invalid (letters, digits, _; must start with a letter)", f.Key)
			continue
		case reservedKeys[f.Key]:
			add("field key %q is reserved", f.Key)
			continue
		case seen[f.Key]:
			add("field key %q is duplicated", f.Key)
			continue
		}
		seen[f.Key] = true
		known[f.Key] = true
		switch f.Type {
		case FieldText, FieldNumber, FieldPassword:
		case FieldSelect:
			if len(f.Options) == 0 {
				add("field %q is a select but has no options", f.Key)
			}
		default:
			add("field %q has unknown type %q", f.Key, f.Type)
		}
		if strings.TrimSpace(f.Label) == "" {
			add("field %q needs a label", f.Key)
		}
	}

	check := func(where, s string) {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			if !known[m[1]] {
				add("%s uses unknown placeholder {{%s}}", where, m[1])
			}
		}
	}
	for k, v := range t.Env {
		check("env "+k, v)
	}
	for _, c := range t.Command {
		check("command", c)
	}
	check("connectionString", t.ConnectionString)

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// ValidateInstanceName checks a container name and that it is not taken.
func ValidateInstanceName(name string, taken []string) error {
	switch {
	case name == "":
		return errors.New("name is required")
	case len(name) > 63:
		return errors.New("name is too long (max 63 characters)")
	case !nameRe.MatchString(name):
		return errors.New("use letters, digits, '.', '_' or '-' (must start with a letter or digit)")
	}
	for _, t := range taken {
		if t == name {
			return fmt.Errorf("a container named %q already exists", name)
		}
	}
	return nil
}

// ValidatePort parses and range-checks a port typed by the user.
func ValidatePort(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, errors.New("port must be a number")
	}
	if p < 1 || p > 65535 {
		return 0, errors.New("port must be between 1 and 65535")
	}
	return p, nil
}

// PortInUse reports whether something on this machine already listens on port.
// Other errors (for example "permission denied" below port 1024) do not prove
// the port is taken, so they count as free; Docker reports the real problem later.
func PortInUse(port int) bool {
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return isAddrInUse(err)
	}
	_ = l.Close()
	return false
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") || // Linux, macOS
		strings.Contains(msg, "only one usage of each socket address") // Windows
}

// ValidateFieldValue checks one wizard answer against its field definition.
func ValidateFieldValue(f Field, v string) error {
	label := f.Label
	if label == "" {
		label = f.Key
	}
	if v == "" {
		if f.Required {
			return fmt.Errorf("%s is required", label)
		}
		return nil
	}
	if f.MinLength > 0 && len(v) < f.MinLength {
		return fmt.Errorf("%s must be at least %d characters", label, f.MinLength)
	}
	switch f.Type {
	case FieldNumber:
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Errorf("%s must be a number", label)
		}
	case FieldSelect:
		for _, o := range f.Options {
			if o == v {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of: %s", label, strings.Join(f.Options, ", "))
	}
	return nil
}

package external

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Store keeps one JSON file per connection in a folder.
// The files hold passwords, so they are written readable by their owner only.
type Store struct {
	dir string
}

// NewStore creates a store whose connections live in dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is the folder holding the connection files.
func (s *Store) Dir() string { return s.dir }

// List returns the valid connections by name. Broken files are skipped and
// reported in the second result, the way the template store does it.
func (s *Store) List() ([]Conn, []error) {
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.json"))
	sort.Strings(files)

	var out []Conn
	var problems []error
	seen := map[string]bool{}
	for _, f := range files {
		c, err := readFile(f)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", filepath.Base(f), err))
			continue
		}
		if seen[c.Name] {
			problems = append(problems, fmt.Errorf("%s: name %q is already used", filepath.Base(f), c.Name))
			continue
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, problems
}

// Get finds a connection by name.
func (s *Store) Get(name string) (Conn, bool) {
	all, _ := s.List()
	for _, c := range all {
		if c.Name == name {
			return c, true
		}
	}
	return Conn{}, false
}

// Names returns every connection name in use.
func (s *Store) Names() []string {
	all, _ := s.List()
	names := make([]string, 0, len(all))
	for _, c := range all {
		names = append(names, c.Name)
	}
	return names
}

// Save writes a connection, replacing an earlier one with the same name.
func (s *Store) Save(c Conn) error {
	if err := Validate(c); err != nil {
		return err
	}
	return writeFile(filepath.Join(s.dir, c.Name+".json"), c)
}

// Delete removes a connection.
func (s *Store) Delete(name string) error {
	err := os.Remove(filepath.Join(s.dir, name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("connection %q not found", name)
	}
	return err
}

func readFile(path string) (Conn, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Conn{}, err
	}
	var c Conn
	if err := json.Unmarshal(data, &c); err != nil {
		return Conn{}, fmt.Errorf("not valid connection JSON: %w", err)
	}
	if err := Validate(c); err != nil {
		return Conn{}, err
	}
	return c, nil
}

func writeFile(path string, c Conn) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the file holds a password. WriteFile keeps the mode of a file that
	// already exists, so a file made by hand, or copied in, is tightened here too.
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

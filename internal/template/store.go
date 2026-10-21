package template

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed builtin/*.json
var builtinFS embed.FS

// Order in which built-in templates appear; unknown ones follow alphabetically.
var builtinOrder = []string{"postgres", "redis", "mysql", "mongodb"}

// ErrBuiltin is returned when someone tries to change a built-in template.
var ErrBuiltin = errors.New("built-in templates cannot be changed; duplicate it first")

// Store reads built-in templates (embedded) and user templates (JSON files in a folder).
type Store struct {
	dir string
}

// NewStore creates a store whose user templates live in dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is the folder holding user templates.
func (s *Store) Dir() string { return s.dir }

// Builtin returns the embedded templates in display order.
func Builtin() ([]Template, error) {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		data, err := builtinFS.ReadFile("builtin/" + e.Name())
		if err != nil {
			return nil, err
		}
		var t Template
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("builtin %s: %w", e.Name(), err)
		}
		if err := ValidateTemplate(t); err != nil {
			return nil, fmt.Errorf("builtin %s: %w", e.Name(), err)
		}
		t.Builtin = true
		out = append(out, t)
	}
	rank := func(id string) int {
		for i, x := range builtinOrder {
			if x == id {
				return i
			}
		}
		return len(builtinOrder)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].ID), rank(out[j].ID)
		if ri != rj {
			return ri < rj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// List returns built-in templates followed by valid user templates.
// Broken user files are skipped and reported in the second result.
func (s *Store) List() ([]Template, []error) {
	all, err := Builtin()
	var problems []error
	if err != nil {
		problems = append(problems, err)
	}
	taken := map[string]bool{}
	for _, t := range all {
		taken[t.ID] = true
	}

	files, _ := filepath.Glob(filepath.Join(s.dir, "*.json"))
	sort.Strings(files)
	for _, f := range files {
		t, err := readFile(f)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", filepath.Base(f), err))
			continue
		}
		if taken[t.ID] {
			problems = append(problems, fmt.Errorf("%s: id %q is already used", filepath.Base(f), t.ID))
			continue
		}
		taken[t.ID] = true
		all = append(all, t)
	}
	return all, problems
}

// Get finds a template by id.
func (s *Store) Get(id string) (Template, bool) {
	all, _ := s.List()
	for _, t := range all {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// IDs returns every template id currently in use.
func (s *Store) IDs() []string {
	all, _ := s.List()
	ids := make([]string, 0, len(all))
	for _, t := range all {
		ids = append(ids, t.ID)
	}
	return ids
}

// Save writes a user template, replacing an earlier version with the same id.
func (s *Store) Save(t Template) error {
	if err := ValidateTemplate(t); err != nil {
		return err
	}
	if isBuiltinID(t.ID) {
		return ErrBuiltin
	}
	t.Builtin = false
	return writeFile(filepath.Join(s.dir, t.ID+".json"), t)
}

// Delete removes a user template.
func (s *Store) Delete(id string) error {
	if isBuiltinID(id) {
		return ErrBuiltin
	}
	err := os.Remove(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("template %q not found", id)
	}
	return err
}

// Export writes any template (built-in or custom) to dst as JSON.
func (s *Store) Export(id, dst string) error {
	t, ok := s.Get(id)
	if !ok {
		return fmt.Errorf("template %q not found", id)
	}
	return writeFile(expandHome(dst), t)
}

// Import reads a template JSON file and saves it as a user template.
// It refuses to overwrite an existing template.
func (s *Store) Import(src string) (Template, error) {
	t, err := readFile(expandHome(src))
	if err != nil {
		return Template{}, err
	}
	if _, exists := s.Get(t.ID); exists {
		return Template{}, fmt.Errorf("a template with id %q already exists", t.ID)
	}
	if err := s.Save(t); err != nil {
		return Template{}, err
	}
	return t, nil
}

func isBuiltinID(id string) bool {
	all, _ := Builtin()
	for _, t := range all {
		if t.ID == id {
			return true
		}
	}
	return false
}

func readFile(path string) (Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	var t Template
	if err := json.Unmarshal(data, &t); err != nil {
		return Template{}, fmt.Errorf("not valid template JSON: %w", err)
	}
	if err := ValidateTemplate(t); err != nil {
		return Template{}, err
	}
	return t, nil
}

func writeFile(path string, t Template) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

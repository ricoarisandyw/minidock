package external

import (
	"os"
	"path/filepath"
	"testing"
)

func sample() Conn {
	return Conn{Name: "shop", Container: "shop-db-1", Client: "mysql", User: "reader", Password: "s3cret", Database: "shop"}
}

func TestValidate(t *testing.T) {
	if err := Validate(sample()); err != nil {
		t.Fatalf("a complete connection should be valid: %v", err)
	}
	bad := map[string]Conn{
		"no name":        {Container: "c", Client: "mysql", User: "u"},
		"spaces in name": {Name: "my db", Container: "c", Client: "mysql", User: "u"},
		"path in name":   {Name: "../escape", Container: "c", Client: "mysql", User: "u"},
		"no container":   {Name: "n", Client: "mysql", User: "u"},
		"wrong client":   {Name: "n", Container: "c", Client: "postgres", User: "u"},
		"no user":        {Name: "n", Container: "c", Client: "mysql"},
		"flag user":      {Name: "n", Container: "c", Client: "mysql", User: "-x"},
		"flag database":  {Name: "n", Container: "c", Client: "mysql", User: "u", Database: "-x"},
	}
	for why, c := range bad {
		if err := Validate(c); err == nil {
			t.Errorf("%s should be refused: %+v", why, c)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())

	if got, _ := s.List(); len(got) != 0 {
		t.Fatalf("a new store should be empty, got %d", len(got))
	}
	if _, ok := s.Get("shop"); ok {
		t.Error("Get should not find anything yet")
	}

	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("shop")
	if !ok || got.Password != "s3cret" || got.Container != "shop-db-1" {
		t.Fatalf("round trip lost data: %+v (found %v)", got, ok)
	}

	// Saving again replaces, it does not duplicate.
	updated := sample()
	updated.Note = "read replica"
	if err := s.Save(updated); err != nil {
		t.Fatal(err)
	}
	all, problems := s.List()
	if len(all) != 1 || len(problems) != 0 {
		t.Fatalf("expected one connection, got %d (%v)", len(all), problems)
	}
	if all[0].Note != "read replica" {
		t.Errorf("note not saved: %+v", all[0])
	}

	if err := s.Delete("shop"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("shop"); err == nil {
		t.Error("deleting twice should report that it is not there")
	}
}

// The file holds a password, so it must not be readable by anyone else.
func TestSavedFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "shop.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("mode = %o, want no access for group or others", perm)
	}
}

// A broken file is reported, and the good ones are still listed.
func TestBrokenFileIsReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	all, problems := s.List()
	if len(all) != 1 {
		t.Errorf("good connection should survive, got %d", len(all))
	}
	if len(problems) != 1 {
		t.Errorf("broken file should be reported, got %v", problems)
	}
}

func TestNamesAreSorted(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, n := range []string{"zeta", "alpha", "mid"} {
		c := sample()
		c.Name = n
		if err := s.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Names()
	want := []string{"alpha", "mid", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

// A file that already exists with loose permissions is tightened when saved over,
// not left readable by everyone.
func TestSavingOverALoosePermissionFileTightensIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shop.json")
	if err := os.WriteFile(path, []byte(`{"name":"shop","container":"c","client":"mysql","user":"u"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(dir).Save(sample()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("mode = %o, want no access for group or others", perm)
	}
}

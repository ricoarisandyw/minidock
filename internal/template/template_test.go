package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustGet(t *testing.T, id string) Template {
	t.Helper()
	all, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range all {
		if tpl.ID == id {
			return tpl
		}
	}
	t.Fatalf("builtin %q not found", id)
	return Template{}
}

func TestBuiltinTemplatesAreValidAndOrdered(t *testing.T) {
	all, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tpl := range all {
		ids = append(ids, tpl.ID)
		if !tpl.Builtin {
			t.Errorf("%s should be marked builtin", tpl.ID)
		}
	}
	got := strings.Join(ids[:4], ",")
	if got != "postgres,redis,mysql,mongodb" {
		t.Errorf("unexpected order: %s", got)
	}
}

func TestRenderPostgres(t *testing.T) {
	pg := mustGet(t, "postgres")
	spec, err := Render(pg, Values{"name": "pg-dev", "version": "16", "port": "5433", "password": "s3cret", "user": "me", "database": "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Image != "postgres:16" || spec.HostPort != 5433 || spec.ContainerPort != 5432 {
		t.Errorf("bad spec: %+v", spec)
	}
	want := []string{"POSTGRES_DB=shop", "POSTGRES_PASSWORD=s3cret", "POSTGRES_USER=me"}
	if strings.Join(spec.Env, ",") != strings.Join(want, ",") {
		t.Errorf("env = %v, want %v", spec.Env, want)
	}
	if spec.Volume != "minidock-pg-dev-data" || spec.DataPath != "/var/lib/postgresql/data" {
		t.Errorf("bad volume: %q %q", spec.Volume, spec.DataPath)
	}
	if spec.Connection != "postgresql://me:s3cret@localhost:5433/shop" {
		t.Errorf("connection = %q", spec.Connection)
	}
}

func TestRenderUsesDefaults(t *testing.T) {
	spec, err := Render(mustGet(t, "mysql"), Values{"name": "db"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Image != "mysql:8.4" || spec.HostPort != 3306 {
		t.Errorf("defaults not applied: %+v", spec)
	}
	if spec.Connection != "mysql://root:mysql@localhost:3306/app" {
		t.Errorf("connection = %q", spec.Connection)
	}
}

func TestRenderRedisCommand(t *testing.T) {
	spec, err := Render(mustGet(t, "redis"), Values{"name": "cache", "password": "pw12"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Cmd, " ") != "redis-server --requirepass pw12 --appendonly yes" {
		t.Errorf("cmd = %v", spec.Cmd)
	}
	if spec.Connection != "redis://:pw12@localhost:6379" {
		t.Errorf("connection = %q", spec.Connection)
	}
}

func TestConnectionStringEscapesSpecialCharacters(t *testing.T) {
	pg := mustGet(t, "postgres")
	got := ConnectionString(pg, Values{"password": "p@ss w/rd:1", "port": "5432"})
	want := "postgresql://postgres:p%40ss%20w%2Frd%3A1@localhost:5432/app"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	pg := mustGet(t, "postgres")
	cases := map[string]Values{
		"empty name":     {"name": ""},
		"bad port":       {"name": "x", "port": "abc"},
		"port too big":   {"name": "x", "port": "70000"},
		"short password": {"name": "x", "password": "ab"},
		"bad name":       {"name": "bad name!"},
	}
	for label, v := range cases {
		if _, err := Render(pg, v); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestPreviewHidesSecretsAndQuotes(t *testing.T) {
	pg := mustGet(t, "postgres")
	vals := Values{"name": "pg", "password": "it's secret"}
	spec, err := Render(pg, vals)
	if err != nil {
		t.Fatal(err)
	}
	out := spec.Preview()
	if strings.Contains(out, "secret") {
		t.Errorf("password leaked in preview:\n%s", out)
	}
	if !strings.Contains(out, "POSTGRES_PASSWORD=****") || !strings.Contains(out, "-p 127.0.0.1:5432:5432") {
		t.Errorf("unexpected preview:\n%s", out)
	}
}

func TestPreviewOnlyHidesThePassword(t *testing.T) {
	// The password equals the name, image and user: only the password slot may be hidden.
	pg := mustGet(t, "postgres")
	vals := Values{"name": "postgres", "password": "postgres"}
	spec, err := Render(pg, vals)
	if err != nil {
		t.Fatal(err)
	}
	out := spec.Preview()
	for _, want := range []string{"--name postgres", "POSTGRES_USER=postgres", "POSTGRES_PASSWORD=****", "postgres:17", "minidock-postgres-data:/var/lib/postgresql/data"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview should contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(spec.Preview(), "PASSWORD=postgres") {
		t.Error("password leaked")
	}
}

func TestValidateTemplateCatchesProblems(t *testing.T) {
	bad := Template{
		ID: "Bad ID", Image: "", ContainerPort: 0,
		Fields: []Field{
			{Key: "a", Label: "A", Type: FieldText},
			{Key: "a", Label: "A", Type: FieldText},
			{Key: "port", Label: "P", Type: FieldText},
			{Key: "pick", Label: "Pick", Type: FieldSelect},
		},
		Env: map[string]string{"X": "{{nope}}"},
	}
	err := ValidateTemplate(bad)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"id must be", "name is required", "image is required", "containerPort", "duplicated", "reserved", "no options", "{{nope}}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestValidateInstanceNameAndPort(t *testing.T) {
	if err := ValidateInstanceName("ok-name_1", nil); err != nil {
		t.Error(err)
	}
	if err := ValidateInstanceName("dup", []string{"dup"}); err == nil {
		t.Error("expected duplicate error")
	}
	if err := ValidateInstanceName("-bad", nil); err == nil {
		t.Error("expected bad name error")
	}
	if _, err := ValidatePort("0"); err == nil {
		t.Error("port 0 should fail")
	}
	if p, err := ValidatePort(" 8080 "); err != nil || p != 8080 {
		t.Errorf("got %d, %v", p, err)
	}
}

func TestStoreSaveListDeleteExportImport(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "templates"))

	custom := Template{
		ID: "my-nginx", Name: "My Nginx", Image: "nginx", ContainerPort: 80,
		Versions: []string{"alpine"},
	}
	if err := s.Save(custom); err != nil {
		t.Fatal(err)
	}
	all, problems := s.List()
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	last := all[len(all)-1]
	if last.ID != "my-nginx" || last.Builtin {
		t.Errorf("custom template missing or marked builtin: %+v", last)
	}

	// Export, delete, then import brings it back unchanged.
	exported := filepath.Join(dir, "out", "nginx.json")
	if err := s.Export("my-nginx", exported); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("my-nginx"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("my-nginx"); ok {
		t.Fatal("template should be gone")
	}
	got, err := s.Import(exported)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "my-nginx" || got.Image != "nginx" || got.ContainerPort != 80 {
		t.Errorf("import changed data: %+v", got)
	}
	if _, err := s.Import(exported); err == nil {
		t.Error("second import should fail (already exists)")
	}
}

func TestStoreProtectsBuiltinsAndSkipsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Save(Template{ID: "postgres", Name: "x", Image: "x", ContainerPort: 1}); err != ErrBuiltin {
		t.Errorf("saving over builtin: got %v", err)
	}
	if err := s.Delete("redis"); err != ErrBuiltin {
		t.Errorf("deleting builtin: got %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, problems := s.List()
	if len(problems) != 1 {
		t.Errorf("expected 1 problem, got %v", problems)
	}
	if len(all) < 4 {
		t.Errorf("builtins should still load, got %d", len(all))
	}
	if err := s.Export("postgres", filepath.Join(dir, "pg.json")); err != nil {
		t.Errorf("builtins should be exportable: %v", err)
	}
}

func TestTextRoundTrips(t *testing.T) {
	fields := mustGet(t, "postgres").Fields
	back, err := ParseFields(FieldsText(fields))
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(fields) {
		t.Fatalf("got %d fields, want %d", len(back), len(fields))
	}
	for i := range fields {
		if back[i].Key != fields[i].Key || back[i].Type != fields[i].Type ||
			back[i].Default != fields[i].Default || back[i].Required != fields[i].Required ||
			back[i].MinLength != fields[i].MinLength {
			t.Errorf("field %d changed: %+v vs %+v", i, back[i], fields[i])
		}
	}

	env, err := ParseEnv("# comment\nA=1\nB = two words\n")
	if err != nil || env["A"] != "1" || env["B"] != "two words" {
		t.Errorf("env parse: %v %v", env, err)
	}
	if _, err := ParseEnv("oops"); err == nil {
		t.Error("expected env error")
	}
	if got := ParseVersions(" 17, 16 ,,15"); strings.Join(got, "|") != "17|16|15" {
		t.Errorf("versions: %v", got)
	}

	args := SplitCommand(`redis-server --requirepass "a b" --x`)
	if strings.Join(args, "|") != "redis-server|--requirepass|a b|--x" {
		t.Errorf("split: %q", args)
	}
	if again := SplitCommand(JoinCommand(args)); strings.Join(again, "|") != strings.Join(args, "|") {
		t.Errorf("join/split not symmetric: %q", again)
	}
}

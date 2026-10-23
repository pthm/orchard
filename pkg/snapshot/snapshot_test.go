package snapshot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindProject(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ProjectDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(root, "scenarios", "nested", "dev.hcl")
	if err := os.MkdirAll(filepath.Dir(scenario), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scenario, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, start := range []string{scenario, filepath.Dir(scenario), root} {
		got, ok := FindProject(start)
		if !ok || got != root {
			t.Errorf("FindProject(%s) = %q, %v; want %q", start, got, ok, root)
		}
	}

	// A file named .orchard doesn't mark a project.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, ProjectDirName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindProject(other); ok && got == other {
		t.Errorf("a .orchard file should not count as a project, got %q", got)
	}
}

func TestPath(t *testing.T) {
	project := t.TempDir()
	got := Path(project, "db")
	if want := filepath.Join(project, ".orchard", "schema", "db.json"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "db.json")
	in := &Snapshot{
		Kind:       "database",
		Target:     "postgres/app",
		Provider:   Provider{Source: "builtin/postgres", Alias: "db"},
		CapturedAt: time.Date(2026, 10, 23, 10, 30, 0, 0, time.UTC),
		Body:       json.RawMessage(`{"tables":{}}`),
	}
	if err := Write(path, in); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "}\n") || !strings.Contains(string(data), "\n  \"kind\": \"database\"") {
		t.Fatalf("expected indented JSON with a trailing newline:\n%s", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}

	out, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Format != Format || out.Kind != in.Kind || out.Target != in.Target ||
		out.Provider != in.Provider || !out.CapturedAt.Equal(in.CapturedAt) {
		t.Fatalf("round trip: %+v", out)
	}
}

func TestReadMissingAndUnknownFormat(t *testing.T) {
	dir := t.TempDir()
	if _, err := Read(filepath.Join(dir, "none.json")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: want ErrNotFound, got %v", err)
	}

	path := filepath.Join(dir, "future.json")
	if err := os.WriteFile(path, []byte(`{"format":"99","kind":"database","body":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), `format "99" not supported`) {
		t.Fatalf("unknown format: got %v", err)
	}
}

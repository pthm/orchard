package record

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
)

func TestNewIDSortableAndUnique(t *testing.T) {
	now := time.Date(2026, 4, 14, 20, 15, 30, 0, time.UTC)
	a := NewID(now)
	b := NewID(now)
	if a == b {
		t.Fatalf("two IDs generated at the same time collided: %s", a)
	}
	// Both IDs share a timestamp prefix regardless of random suffix,
	// so lexicographic comparison to any earlier ID shows the earlier
	// one first.
	earlier := NewID(now.Add(-time.Minute))
	if earlier >= a || earlier >= b {
		t.Errorf("earlier ID should sort before later: earlier=%s a=%s b=%s", earlier, a, b)
	}
	if !strings.HasPrefix(a, "20260414T201530Z_") {
		t.Errorf("unexpected ID format: %s", a)
	}
}

func TestFilenameSanitisesScenarioName(t *testing.T) {
	cases := map[string]string{
		"seed_dev":       "orchard_seed_dev_run_X.json",
		"Seed Dev":       "orchard_Seed_Dev_run_X.json",
		"demo/case:2":    "orchard_demo_case_2_run_X.json",
		"":               "orchard_unnamed_run_X.json",
		"  ":             "orchard____run_X.json",
		"name.with.dots": "orchard_name_with_dots_run_X.json",
	}
	for in, want := range cases {
		got := Filename(in, "X")
		if got != want {
			t.Errorf("Filename(%q): got %q want %q", in, got, want)
		}
	}
}

func TestFileWriterAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	fw := FileWriter{Dir: dir}
	rec := &Record{
		Format:     Format,
		ID:         "20260414T000000Z_abc",
		Scenario:   Scenario{Name: "seed_dev", Path: "/abs/s.hcl"},
		StartedAt:  time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 4, 14, 0, 0, 1, 0, time.UTC),
		Status:     StatusSucceeded,
		Nodes: []Node{{
			ID:         "action.sql.x",
			Kind:       KindAction,
			Status:     StatusSucceeded,
			StartedAt:  time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
			FinishedAt: time.Date(2026, 4, 14, 0, 0, 1, 0, time.UTC),
		}},
	}
	if err := fw.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// No temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 {
		t.Fatalf("expected exactly one file, got %v", names)
	}
	if !strings.HasPrefix(names[0], "orchard_seed_dev_run_") || !strings.HasSuffix(names[0], ".json") {
		t.Errorf("unexpected filename: %q", names[0])
	}

	// Round-trip: read + decode matches what we wrote.
	raw, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	var got Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != rec.ID || got.Status != rec.Status || len(got.Nodes) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestEncodeCtyPreservesTypes(t *testing.T) {
	in := map[string]cty.Value{
		"id":    cty.NumberIntVal(42),
		"name":  cty.StringVal("Acme"),
		"tags":  cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}),
		"empty": cty.NullVal(cty.String),
	}
	out, err := EncodeCty(in)
	if err != nil {
		t.Fatalf("EncodeCty: %v", err)
	}
	for k := range in {
		if _, ok := out[k]; !ok {
			t.Errorf("missing key %q in encoded output", k)
		}
	}
	// An empty map encodes to nil so omitempty fires.
	empty, err := EncodeCty(nil)
	if err != nil || empty != nil {
		t.Errorf("EncodeCty(nil): got %v, err=%v", empty, err)
	}
}

func writeRecord(t *testing.T, dir string, rec *Record) string {
	t.Helper()
	path := filepath.Join(dir, Filename(rec.Scenario.Name, rec.ID))
	if err := Save(path, rec); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRefusesOtherFormats(t *testing.T) {
	dir := t.TempDir()
	for _, format := range []string{"", "2"} {
		path := filepath.Join(dir, "r.json")
		if err := os.WriteFile(path, []byte(`{"format":"`+format+`","id":"x"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), `"`+format+`"`) || !strings.Contains(err.Error(), `want "1"`) {
			t.Errorf("format %q: want refusal naming both formats, got %v", format, err)
		}
	}
}

// TestLoadOldRecord: a record from before torn_down / torn_down_at
// still loads, with both unset.
func TestLoadOldRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.json")
	old := `{"format":"1","id":"20260414T000000Z_abc","scenario":{"name":"s","path":"/s.hcl"},
"started_at":"2026-04-14T00:00:00Z","finished_at":"2026-04-14T00:00:01Z","status":"succeeded",
"nodes":[{"id":"action.sql.x","kind":"action","started_at":"2026-04-14T00:00:00Z","finished_at":"2026-04-14T00:00:01Z","status":"succeeded"}]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.TornDownAt != nil || rec.Nodes[0].TornDown {
		t.Errorf("old record should load with no teardown progress: %+v", rec)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	at := time.Date(2026, 4, 14, 1, 2, 3, 0, time.UTC)
	rec := &Record{
		Format: Format, ID: "20260414T000000Z_abc", Scenario: Scenario{Name: "s"},
		Nodes:      []Node{{ID: "action.sql.x", TornDown: true}},
		TornDownAt: &at,
	}
	got, err := Load(writeRecord(t, t.TempDir(), rec))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Nodes[0].TornDown || got.TornDownAt == nil || !got.TornDownAt.Equal(at) {
		t.Errorf("round-trip lost teardown progress: %+v", got)
	}
}

// TestListOrdersByStartedAt: two runs in the same second share an ID
// prefix, so order comes from StartedAt, not the random suffix.
func TestListOrdersByStartedAt(t *testing.T) {
	dir := t.TempDir()
	sec := time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)
	writeRecord(t, dir, &Record{Format: Format, ID: "20260414T000000Z_ffffffff", Scenario: Scenario{Name: "s"}, StartedAt: sec.Add(100 * time.Millisecond)})
	writeRecord(t, dir, &Record{Format: Format, ID: "20260414T000000Z_00000000", Scenario: Scenario{Name: "s"}, StartedAt: sec.Add(900 * time.Millisecond)})
	if err := os.WriteFile(filepath.Join(dir, "orchard_s_run_garbage.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warn strings.Builder
	files, err := List(dir, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Record.ID != "20260414T000000Z_00000000" || files[1].Record.ID != "20260414T000000Z_ffffffff" {
		t.Fatalf("want newest StartedAt first, got %+v", files)
	}
	if !strings.Contains(warn.String(), "orchard_s_run_garbage.json") {
		t.Errorf("bad file should be reported, warn: %q", warn.String())
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	path := writeRecord(t, dir, &Record{Format: Format, ID: "20260414T000000Z_abcdef01", Scenario: Scenario{Name: "s"}})

	byPath, err := Resolve("elsewhere", path, io.Discard)
	if err != nil || byPath.Path != path {
		t.Errorf("by path: %+v, %v", byPath, err)
	}
	byID, err := Resolve(dir, "20260414T000000Z_abcdef01", io.Discard)
	if err != nil || byID.Path != path {
		t.Errorf("by ID: %+v, %v", byID, err)
	}
	// A record that can't load is reported, not hidden behind the
	// not-found error.
	if err := os.WriteFile(filepath.Join(dir, "orchard_s_run_garbage.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warn strings.Builder
	if _, err := Resolve(dir, "20260414T000000Z_nope", &warn); err == nil || !strings.Contains(err.Error(), dir) {
		t.Errorf("unknown ID should name the dir, got %v", err)
	}
	if !strings.Contains(warn.String(), "orchard_s_run_garbage.json") {
		t.Errorf("bad file should be reported, warn: %q", warn.String())
	}
}

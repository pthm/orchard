package record

import (
	"encoding/json"
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

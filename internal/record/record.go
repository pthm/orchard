// Package record defines the on-disk run record Orchard writes after
// each `orchard run`. A record captures what a scenario did well
// enough that future tooling — starting with `orchard teardown` —
// can reason about it without re-executing the scenario.
//
// The format is JSON, pretty-printed for inspectability. cty values
// (variables, node outputs, scenario outputs) serialise through
// pkg/protocol/ctyjson so they round-trip cleanly with the engine's
// own type system.
//
// This is deliberately a simpler model than Terraform's state: no
// remote backends, no locking, no versioning, one file per run in
// the user's working directory. That keeps the surface small while
// the teardown story is being prototyped. A records-based teardown
// can grow sophistication when real-world use demands it.
package record

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// Format is a monotonically-increasing record format version. Bump
// only for breaking changes to the on-disk shape: Load refuses any
// other format, so additive fields (omitempty, absent in older
// records) stay on the current one.
const Format = "1"

// Status values for runs and nodes.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
)

// Kind values for node entries.
const (
	KindAction    = "action"
	KindComponent = "component"
)

// Record is the top-level document persisted per run.
type Record struct {
	Format     string                     `json:"format"`
	ID         string                     `json:"id"`
	Scenario   Scenario                   `json:"scenario"`
	StartedAt  time.Time                  `json:"started_at"`
	FinishedAt time.Time                  `json:"finished_at"`
	Status     string                     `json:"status"`
	Error      string                     `json:"error,omitempty"`
	Variables  map[string]json.RawMessage `json:"variables,omitempty"`
	// Seed is the run's random seed; `orchard run --seed` with it
	// draws the same random values again. Absent in older records.
	Seed    *uint64                    `json:"seed,omitempty"`
	Nodes   []Node                     `json:"nodes"`
	Outputs map[string]json.RawMessage `json:"outputs,omitempty"`
	// TornDownAt is set once `orchard teardown` has finished every
	// node's teardown; such a record is refused by a later teardown.
	TornDownAt *time.Time `json:"torn_down_at,omitempty"`
}

// Scenario is the subset of scenario metadata worth persisting. Path
// is absolute so a record is self-describing even when the consumer
// runs from a different cwd than the original `orchard run`.
type Scenario struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Node captures one graph node's execution. Outputs is only populated
// for succeeded nodes; Error is only populated for failed ones.
// Skipped nodes (upstream of a failure) have neither.
type Node struct {
	ID         string                     `json:"id"`
	Kind       string                     `json:"kind"`
	StartedAt  time.Time                  `json:"started_at"`
	FinishedAt time.Time                  `json:"finished_at"`
	Status     string                     `json:"status"`
	Outputs    map[string]json.RawMessage `json:"outputs,omitempty"`
	Error      string                     `json:"error,omitempty"`
	// Undo is how the node's provider says to reverse it (a typed
	// insert: delete the row by its primary key). `orchard teardown`
	// runs it when the action has no explicit teardown block. Records
	// from before undo existed simply don't have it.
	Undo *Undo `json:"undo,omitempty"`
	// TornDown marks a node whose teardown succeeded, so re-running a
	// partly failed teardown skips it.
	TornDown bool `json:"torn_down,omitempty"`
}

// Undo is a recorded provider.Invocation: an action and its evaluated
// attributes.
type Undo struct {
	Action string                     `json:"action"`
	Attrs  map[string]json.RawMessage `json:"attrs"`
}

// EncodeCty serialises a map of cty values using ctyjson, preserving
// type information across the JSON round-trip. Returns nil for an
// empty input so omitempty fires and the serialised record stays
// compact.
func EncodeCty(values map[string]cty.Value) (map[string]json.RawMessage, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(values))
	for k, v := range values {
		raw, err := protocol.MarshalValue(v)
		if err != nil {
			return nil, err
		}
		out[k] = raw
	}
	return out, nil
}

// DecodeCty is the inverse of EncodeCty: turn a map of JSON-encoded
// cty values back into cty values. Types are inferred from the JSON
// structure (DynamicPseudoType) since the record doesn't carry a
// type hint per field — numbers become numbers, lists become tuples,
// objects become objects. Close enough for teardown expressions that
// typically read scalars and occasionally traverse an object.
func DecodeCty(raw map[string]json.RawMessage) (map[string]cty.Value, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]cty.Value, len(raw))
	for k, v := range raw {
		val, err := protocol.UnmarshalValue(v, cty.DynamicPseudoType)
		if err != nil {
			return nil, err
		}
		out[k] = val
	}
	return out, nil
}

// IndexOutputs decodes each succeeded node's outputs into a map
// keyed by full node ID ("action.sql.create_merchant" etc.). Failed
// or output-less nodes are omitted — callers treat their absence as
// "nothing to tear down". Designed as the canonical way to hand a
// record off to engine.Teardown.
func IndexOutputs(r *Record) (map[string]map[string]cty.Value, error) {
	out := make(map[string]map[string]cty.Value, len(r.Nodes))
	for _, n := range r.Nodes {
		if n.Status != StatusSucceeded || len(n.Outputs) == 0 {
			continue
		}
		m, err := DecodeCty(n.Outputs)
		if err != nil {
			return nil, err
		}
		out[n.ID] = m
	}
	return out, nil
}

// IndexUndos decodes each succeeded node's undo invocation, keyed by
// full node ID, for engine.Teardown. Nodes without one are omitted.
func IndexUndos(r *Record) (map[string]provider.Invocation, error) {
	out := map[string]provider.Invocation{}
	for _, n := range r.Nodes {
		if n.Status != StatusSucceeded || n.Undo == nil {
			continue
		}
		attrs, err := DecodeCty(n.Undo.Attrs)
		if err != nil {
			return nil, fmt.Errorf("%s: undo: %w", n.ID, err)
		}
		out[n.ID] = provider.Invocation{Action: n.Undo.Action, Attrs: attrs}
	}
	return out, nil
}

// Load reads and parses a record file from disk, refusing a format
// other than Format.
func Load(path string) (*Record, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	if rec.Format != Format {
		return nil, fmt.Errorf("%s: record format %q, want %q", path, rec.Format, Format)
	}
	return &rec, nil
}

// Save writes rec to path through a sibling temp file and a rename,
// so a partially-flushed record can't appear on disk.
func Save(path string, rec *Record) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".orchard-record-*.json.tmp")
	if err != nil {
		return fmt.Errorf("record: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("record: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("record: close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("record: rename: %w", err)
	}
	return nil
}

// File is a record and the path it was loaded from.
type File struct {
	Path   string
	Record *Record
}

// List loads every record in dir, newest first. Files that match the
// record name but can't be loaded (unreadable, not JSON, an unknown
// format) are skipped with a line on warn.
func List(dir string, warn io.Writer) ([]File, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "orchard_*_run_*.json"))
	if err != nil {
		return nil, err
	}
	var files []File
	for _, p := range paths {
		rec, err := Load(p)
		if err != nil {
			_, _ = fmt.Fprintf(warn, "warning: skipping record %s: %v\n", p, err)
			continue
		}
		files = append(files, File{Path: p, Record: rec})
	}
	// IDs only carry the second, so two runs in one second need
	// StartedAt's full precision; the ID breaks exact ties.
	slices.SortFunc(files, func(a, b File) int {
		if c := b.Record.StartedAt.Compare(a.Record.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(b.Record.ID, a.Record.ID)
	})
	return files, nil
}

// Resolve finds a record from a command-line argument: a path to an
// existing file, or else a run ID looked up in dir. Records in dir
// that can't be loaded are reported on warn, as List does.
func Resolve(dir, arg string, warn io.Writer) (File, error) {
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		rec, err := Load(arg)
		if err != nil {
			return File{}, err
		}
		return File{Path: arg, Record: rec}, nil
	}
	// ponytail: loads every record in dir to match one ID; index by
	// filename if record dirs grow to thousands of runs.
	files, err := List(dir, warn)
	if err != nil {
		return File{}, err
	}
	for _, f := range files {
		if f.Record.ID == arg {
			return f, nil
		}
	}
	return File{}, fmt.Errorf("no record file %s, and no run with that ID in %s", arg, dir)
}

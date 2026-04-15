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
	"os"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
)

// Format is a monotonically-increasing record format version. Bump
// when making a breaking change to the on-disk shape so readers can
// refuse a record they don't understand.
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
	Nodes      []Node                     `json:"nodes"`
	Outputs    map[string]json.RawMessage `json:"outputs,omitempty"`
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

// Load reads and parses a record file from disk.
func Load(path string) (*Record, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

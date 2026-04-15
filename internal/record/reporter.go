package record

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/parser"
)

// Writer accepts a rendered Record. The default implementation
// writes to a file in a configured directory; tests and future
// alternative backends (remote stores, stdout, etc.) implement it
// too.
type Writer interface {
	Write(r *Record) error
}

// FileWriter persists records to the filesystem. Dir is where the
// file lands; the filename is derived from the scenario name and
// run id. Writes go through a temp file + atomic rename so a
// partially-flushed record can't appear on disk.
type FileWriter struct {
	Dir string
}

// Write implements Writer.
func (fw FileWriter) Write(r *Record) error {
	if fw.Dir == "" {
		return fmt.Errorf("record: FileWriter.Dir is required")
	}
	if err := os.MkdirAll(fw.Dir, 0o755); err != nil {
		return fmt.Errorf("record: create dir: %w", err)
	}
	path := filepath.Join(fw.Dir, Filename(r.Scenario.Name, r.ID))

	// Atomic rename: write to a sibling temp file then rename. If
	// the process dies mid-write, there's no half-written target
	// for consumers to mistake as valid.
	tmp, err := os.CreateTemp(fw.Dir, ".orchard-record-*.json.tmp")
	if err != nil {
		return fmt.Errorf("record: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("record: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("record: close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("record: rename: %w", err)
	}
	return nil
}

// Reporter wraps another engine.Reporter and accumulates enough
// state to assemble a Record at the end of the run. Writer is
// called exactly once, from RunCompleted. WriteErr (if the write
// fails) is surfaced through the reporter's own Out writer so the
// user sees it, and the error is retained on the reporter so the
// caller can choose to fail the run.
//
// Wrap a presentation reporter like so:
//
//	base := reporter.Select(reporter.Options{Out: os.Stdout})
//	rec := record.NewReporter(base, scenarioMeta, variables, record.FileWriter{Dir: "."}, os.Stderr)
//	engine.SetReporter(rec)
//
// The inner reporter receives every event first; the recorder
// accumulates its own copy. This keeps presentation identical to a
// no-record run.
type Reporter struct {
	inner    engine.Reporter
	scenario Scenario
	vars     map[string]json.RawMessage
	writer   Writer
	log      io.Writer

	startedAt time.Time

	// pending tracks nodes between NodeStarted and NodeCompleted /
	// NodeFailed so we can compute per-node timing without the
	// engine needing to emit start timestamps. Maps node id to
	// start time. A node completes (or fails) exactly once.
	pending map[string]time.Time

	nodes []Node

	// WriteErr is set if the record could not be persisted.
	WriteErr error
}

// Options configures a Reporter. Most callers only need Writer and
// scenario context; LogOutput is where a write failure surfaces.
type Options struct {
	Inner     engine.Reporter
	Writer    Writer
	Scenario  Scenario
	Variables map[string]cty.Value // resolved scenario variables
	LogOutput io.Writer            // where write failures surface; defaults to os.Stderr
}

// NewReporter constructs a Reporter from options. The scenario
// variables are encoded up front so a later failure during the run
// doesn't lose the inputs that produced it.
func NewReporter(opts Options) (*Reporter, error) {
	if opts.Inner == nil {
		opts.Inner = engine.NopReporter{}
	}
	if opts.LogOutput == nil {
		opts.LogOutput = os.Stderr
	}
	vars, err := EncodeCty(opts.Variables)
	if err != nil {
		return nil, fmt.Errorf("encode variables: %w", err)
	}
	return &Reporter{
		inner:    opts.Inner,
		scenario: opts.Scenario,
		vars:     vars,
		writer:   opts.Writer,
		log:      opts.LogOutput,
		pending:  make(map[string]time.Time),
	}, nil
}

// RunStarted implements engine.Reporter.
func (r *Reporter) RunStarted(scenario string) {
	r.startedAt = time.Now().UTC()
	r.inner.RunStarted(scenario)
}

// NodeStarted implements engine.Reporter.
func (r *Reporter) NodeStarted(nodeID string) {
	r.pending[nodeID] = time.Now().UTC()
	r.inner.NodeStarted(nodeID)
}

// NodeCompleted implements engine.Reporter. Outputs are copied so
// the caller is free to mutate the map after the call returns.
func (r *Reporter) NodeCompleted(nodeID string, outputs map[string]cty.Value, d time.Duration) {
	start := r.pending[nodeID]
	delete(r.pending, nodeID)
	finished := start.Add(d)

	encoded, err := EncodeCty(outputs)
	if err != nil {
		// Outputs we can't encode turn into an empty map on the
		// record. The run succeeded as far as the engine is
		// concerned; surfacing the encode failure in the log lets
		// the user know the record is less useful than expected
		// without failing the run.
		fmt.Fprintf(r.log, "orchard: record: encode outputs for %s: %v\n", nodeID, err)
		encoded = nil
	}

	r.nodes = append(r.nodes, Node{
		ID:         nodeID,
		Kind:       classifyKind(nodeID),
		StartedAt:  start,
		FinishedAt: finished,
		Status:     StatusSucceeded,
		Outputs:    encoded,
	})
	r.inner.NodeCompleted(nodeID, outputs, d)
}

// NodeFailed implements engine.Reporter.
func (r *Reporter) NodeFailed(nodeID string, err error) {
	start, ok := r.pending[nodeID]
	delete(r.pending, nodeID)
	finished := time.Now().UTC()
	if !ok {
		start = finished
	}
	r.nodes = append(r.nodes, Node{
		ID:         nodeID,
		Kind:       classifyKind(nodeID),
		StartedAt:  start,
		FinishedAt: finished,
		Status:     StatusFailed,
		Error:      err.Error(),
	})
	r.inner.NodeFailed(nodeID, err)
}

// RunCompleted implements engine.Reporter. This is where the record
// is assembled and persisted.
func (r *Reporter) RunCompleted(summary engine.RunSummary) {
	r.inner.RunCompleted(summary)

	finished := time.Now().UTC()
	status := StatusSucceeded
	if !summary.OK {
		status = StatusFailed
	}

	outputs, err := EncodeCty(summary.Outputs)
	if err != nil {
		fmt.Fprintf(r.log, "orchard: record: encode scenario outputs: %v\n", err)
		outputs = nil
	}

	rec := &Record{
		Format:     Format,
		ID:         NewID(r.startedAt),
		Scenario:   r.scenario,
		StartedAt:  r.startedAt,
		FinishedAt: finished,
		Status:     status,
		Variables:  r.vars,
		Nodes:      r.nodes,
		Outputs:    outputs,
	}
	if summary.Err != nil {
		rec.Error = summary.Err.Error()
	}

	if r.writer == nil {
		return
	}
	if err := r.writer.Write(rec); err != nil {
		r.WriteErr = err
		fmt.Fprintf(r.log, "orchard: record: %v\n", err)
	}
}

// classifyKind derives the node kind from its ID without requiring
// the engine to pass kind explicitly. IDs use a fixed namespace
// prefix: action.* or component.*.
func classifyKind(nodeID string) string {
	if strings.HasPrefix(nodeID, "component.") && !strings.Contains(nodeID, ".action.") {
		return KindComponent
	}
	if strings.Contains(nodeID, ".action.") || strings.HasPrefix(nodeID, "action.") {
		return KindAction
	}
	return KindAction
}

// ScenarioFromParser extracts the bits of scenario metadata the
// record needs from a ParsedScenario + its source path. Keeps the
// caller from reaching into parser internals.
func ScenarioFromParser(ps *parser.ParsedScenario, path string) Scenario {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return Scenario{
		Name: ps.Scenario.Name,
		Path: abs,
	}
}

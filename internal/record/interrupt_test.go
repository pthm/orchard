package record

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// stallProvider succeeds once, then blocks until ctx is cancelled.
type stallProvider struct {
	calls   int
	started chan struct{}
}

func (s *stallProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "fake", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{"sql": {}},
	}
}
func (s *stallProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (s *stallProvider) Close() error                                          { return nil }

func (s *stallProvider) Execute(ctx context.Context, _ string, _ map[string]cty.Value) (map[string]cty.Value, error) {
	s.calls++
	if s.calls == 1 {
		return map[string]cty.Value{"id": cty.StringVal("1")}, nil
	}
	close(s.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *stallProvider) Inverse(string, map[string]cty.Value, map[string]cty.Value, provider.InverseOptions) *provider.Invocation {
	return &provider.Invocation{Action: "sql_delete", Attrs: map[string]cty.Value{"id": cty.StringVal("1")}}
}

// TestRecordWrittenOnInterrupt: an interrupted run still writes a
// failed record holding the completed node and its undo.
func TestRecordWrittenOnInterrupt(t *testing.T) {
	p := &stallProvider{started: make(chan struct{})}
	reg := provider.NewRegistry()
	if err := reg.Register("fake", p); err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(reg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rep, err := NewReporter(Options{Writer: FileWriter{Dir: dir}, Scenario: Scenario{Name: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	eng.SetReporter(rep)

	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.sql.one": {ID: "action.sql.one", Kind: graph.ActionNode, ActionType: "sql"},
			"action.sql.two": {ID: "action.sql.two", Kind: graph.ActionNode, ActionType: "sql"},
		},
		Order: []graph.NodeID{"action.sql.one", "action.sql.two"},
	}
	ps := &parser.ParsedScenario{Scenario: &parser.ScenarioBlock{Name: "test"}, Variables: map[string]cty.Value{}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-p.started
		cancel()
	}()
	if _, err := eng.Execute(ctx, ps, g); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute: want context.Canceled, got %v", err)
	}

	recs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(recs) != 1 || rep.Path() != recs[0] {
		t.Fatalf("expected one record at %q, got %v", rep.Path(), recs)
	}
	rec, err := Load(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusFailed || len(rec.Nodes) != 2 {
		t.Fatalf("want a failed record with two nodes, got %s with %+v", rec.Status, rec.Nodes)
	}
	one, two := rec.Nodes[0], rec.Nodes[1]
	if one.ID != "action.sql.one" || one.Status != StatusSucceeded || one.Undo == nil || one.Undo.Action != "sql_delete" {
		t.Errorf("node one should succeed with its undo: %+v", one)
	}
	if two.ID != "action.sql.two" || two.Status != StatusFailed {
		t.Errorf("node two should fail: %+v", two)
	}
}

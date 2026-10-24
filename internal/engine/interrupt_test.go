package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// blockingProvider succeeds on its first call and blocks on ctx in
// every later one, closing started when it begins blocking.
type blockingProvider struct {
	calls   int
	started chan struct{}
}

func (b *blockingProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "fake", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{"sql": {}},
	}
}
func (b *blockingProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (b *blockingProvider) Close() error                                          { return nil }

func (b *blockingProvider) Execute(ctx context.Context, _ string, _ map[string]cty.Value) (map[string]cty.Value, error) {
	b.calls++
	if b.calls == 1 {
		return map[string]cty.Value{"id": cty.StringVal("1")}, nil
	}
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingProvider) ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, _ provider.InverseOptions) (map[string]cty.Value, *provider.Invocation, error) {
	out, err := b.Execute(ctx, action, attrs)
	if err != nil {
		return nil, nil, err
	}
	return out, &provider.Invocation{Action: "sql_delete", Attrs: map[string]cty.Value{"id": cty.StringVal("1")}}, nil
}

// captureReporter keeps the events an interrupted run emits.
type captureReporter struct {
	NopReporter
	completed []string
	undos     []string
	failed    []string
	runs      []RunSummary
}

func (c *captureReporter) NodeCompleted(id string, _ map[string]cty.Value, _ time.Duration) {
	c.completed = append(c.completed, id)
}

func (c *captureReporter) NodeUndo(id string, _ provider.Invocation) { c.undos = append(c.undos, id) }

func (c *captureReporter) NodeFailed(id string, _ error) { c.failed = append(c.failed, id) }
func (c *captureReporter) RunCompleted(s RunSummary)     { c.runs = append(c.runs, s) }

// TestExecuteInterrupted: cancelling ctx mid-run fails the node in
// flight through the normal failure path, keeps the completed node
// and its undo, and reports the run as failed once.
func TestExecuteInterrupted(t *testing.T) {
	p := &blockingProvider{started: make(chan struct{})}
	reg := provider.NewRegistry()
	if err := reg.Register("fake", p); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	rep := &captureReporter{}
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
	_, err = eng.Execute(ctx, ps, g)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute: want context.Canceled, got %v", err)
	}
	if len(rep.completed) != 1 || rep.completed[0] != "action.sql.one" ||
		len(rep.undos) != 1 || rep.undos[0] != "action.sql.one" {
		t.Errorf("node one should complete with its undo: completed %v, undos %v", rep.completed, rep.undos)
	}
	if len(rep.failed) != 1 || rep.failed[0] != "action.sql.two" {
		t.Errorf("node two should fail: %v", rep.failed)
	}
	if len(rep.runs) != 1 || rep.runs[0].OK {
		t.Errorf("want one failed RunCompleted, got %+v", rep.runs)
	}
}

// TestExecuteCancelledBeforeNode: a run whose ctx is already cancelled
// stops before calling the provider, even one that ignores ctx.
func TestExecuteCancelledBeforeNode(t *testing.T) {
	reg := provider.NewRegistry()
	p := &blockingProvider{}
	_ = reg.Register("fake", p)
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{"action.sql.one": {ID: "action.sql.one", Kind: graph.ActionNode, ActionType: "sql"}},
		Order: []graph.NodeID{"action.sql.one"},
	}
	ps := &parser.ParsedScenario{Scenario: &parser.ScenarioBlock{Name: "test"}, Variables: map[string]cty.Value{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.Execute(ctx, ps, g); !errors.Is(err, context.Canceled) || p.calls != 0 {
		t.Fatalf("want context.Canceled and no provider calls, got %v after %d calls", err, p.calls)
	}
}

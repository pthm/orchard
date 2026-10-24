package engine

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// hookProvider's step action calls fn with its value, which it echoes
// back, and records an undo of the same step.
type hookProvider struct {
	fn func(ctx context.Context, value string) error
}

func (hookProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "hook", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"step": {Attrs: map[string]protocol.AttrSchema{
				"value": {Type: "string", Required: true},
			}},
		},
	}
}
func (hookProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (hookProvider) Close() error                                          { return nil }

func (h hookProvider) Execute(ctx context.Context, _ string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	v := attrs["value"].AsString()
	if err := h.fn(ctx, v); err != nil {
		return nil, err
	}
	return map[string]cty.Value{"value": attrs["value"]}, nil
}

func (h hookProvider) ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, _ provider.InverseOptions) (map[string]cty.Value, *provider.Invocation, error) {
	out, err := h.Execute(ctx, action, attrs)
	if err != nil {
		return nil, nil, err
	}
	return out, &provider.Invocation{Action: "step", Attrs: attrs}, nil
}

// eventReporter records "start:<id>" and "done:<id>" in event order.
type eventReporter struct {
	captureReporter
	events   []string
	onFailed func()
}

func (r *eventReporter) NodeStarted(id string) { r.events = append(r.events, "start:"+id) }

func (r *eventReporter) NodeCompleted(id string, out map[string]cty.Value, d time.Duration) {
	r.events = append(r.events, "done:"+id)
	r.captureReporter.NodeCompleted(id, out, d)
}

func (r *eventReporter) NodeFailed(id string, err error) {
	r.captureReporter.NodeFailed(id, err)
	if r.onFailed != nil {
		r.onFailed()
	}
}

// runHook runs a scenario body against hookProvider at parallelism n.
func runHook(t *testing.T, body string, n int, fn func(context.Context, string) error, rep Reporter) (*graph.Graph, error) {
	t.Helper()
	dir := t.TempDir()
	writeScenario(t, dir, "comp.hcl", `
component "c" {
  variable "name" { type = string }
  action "step" "inner" { value = var.name }
  output "value" { value = action.step.inner.value }
}`)
	ps, err := parser.LoadScenario(writeScenario(t, dir, "s.hcl", "scenario \"s\" {\n"+body+"\n}"), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(ps)
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	if err := reg.Register("hook", hookProvider{fn: fn}); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	eng.SetParallelism(n)
	eng.SetReporter(rep)
	_, err = eng.Execute(context.Background(), ps, g)
	return g, err
}

// barrier makes every step wait until n steps are in flight at once,
// or until wait passes, and reports the most it saw in flight.
func barrier(n int32, wait time.Duration) (func(context.Context, string) error, *atomic.Int32) {
	var inFlight, most atomic.Int32
	var reached atomic.Bool
	return func(context.Context, string) error {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for m := most.Load(); cur > m && !most.CompareAndSwap(m, cur); m = most.Load() {
		}
		if cur >= n {
			reached.Store(true)
		}
		for deadline := time.Now().Add(wait); !reached.Load() && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		return nil
	}, &most
}

const twoIndependent = `
  action "step" "a" { value = "a" }
  action "step" "b" { value = "b" }`

func TestParallelRunsIndependentActionsTogether(t *testing.T) {
	fn, most := barrier(2, 10*time.Second)
	if _, err := runHook(t, twoIndependent, 2, fn, NopReporter{}); err != nil {
		t.Fatal(err)
	}
	if most.Load() != 2 {
		t.Fatalf("want both actions in flight at once, saw at most %d", most.Load())
	}
}

func TestParallelismOneRunsOneAtATime(t *testing.T) {
	fn, most := barrier(2, 50*time.Millisecond)
	if _, err := runHook(t, twoIndependent, 1, fn, NopReporter{}); err != nil {
		t.Fatal(err)
	}
	if most.Load() != 1 {
		t.Fatalf("want one action in flight at a time, saw %d", most.Load())
	}
}

// mixed has components, count instances and depends_on, so g.Order
// interleaves independent branches.
const mixed = `
  action "step" "root" { value = "root" }
  action "step" "item" {
    count = 4
    value = "item-${count.index}-${action.step.root.value}"
  }
  component "c" "site" {
    source   = "./comp.hcl"
    for_each = ["eu", "us"]
    inputs   = { name = each.key }
  }
  action "step" "late" {
    value      = "late"
    depends_on = [component.c.site]
  }
  action "step" "free" { value = "free" }
  action "step" "summary" {
    value = join(",", concat(action.step.item[*].value, [component.c.site["eu"].value, action.step.late.value]))
  }
  output "summary" { value = action.step.summary.value }`

func TestParallelismOneMatchesGraphOrder(t *testing.T) {
	rep := &eventReporter{}
	g, err := runHook(t, mixed, 1, func(context.Context, string) error { return nil }, rep)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, 2*len(g.Order))
	for _, id := range g.Order {
		want = append(want, "start:"+string(id), "done:"+string(id))
	}
	if !slices.Equal(rep.events, want) {
		t.Fatalf("events:\n got %v\nwant %v", rep.events, want)
	}
}

func TestParallelNeverStartsBeforeDependencies(t *testing.T) {
	sleepy := func(context.Context, string) error {
		time.Sleep(time.Duration(rand.IntN(500)) * time.Microsecond)
		return nil
	}
	for range 50 {
		rep := &eventReporter{}
		g, err := runHook(t, mixed, 8, sleepy, rep)
		if err != nil {
			t.Fatal(err)
		}
		for id, node := range g.Nodes {
			started := slices.Index(rep.events, "start:"+string(id))
			if started < 0 {
				t.Fatalf("%s never started: %v", id, rep.events)
			}
			for _, dep := range node.Dependencies {
				if done := slices.Index(rep.events, "done:"+string(dep)); done < 0 || done > started {
					t.Fatalf("%s started before %s completed: %v", id, dep, rep.events)
				}
			}
		}
	}
}

// TestParallelFailureDrainsInFlight: when b fails while a is in
// flight, a still completes with its undo, c (queued) never starts,
// and the run is reported failed once.
func TestParallelFailureDrainsInFlight(t *testing.T) {
	failed := make(chan struct{})
	rep := &eventReporter{onFailed: func() { close(failed) }}
	fn := func(ctx context.Context, v string) error {
		switch v {
		case "a":
			select {
			case <-failed:
			case <-time.After(10 * time.Second):
				return errors.New("timed out waiting for b to fail")
			}
			// Give a run that cancels on failure time to do it.
			time.Sleep(20 * time.Millisecond)
			if err := ctx.Err(); err != nil {
				return err
			}
		case "b":
			return errors.New("boom")
		}
		return nil
	}
	_, err := runHook(t, twoIndependent+`
  action "step" "c" { value = "c" }`, 2, fn, rep)
	if err == nil || len(rep.failed) != 1 || rep.failed[0] != "action.step.b" {
		t.Fatalf("want b to fail, got err %v, failed %v", err, rep.failed)
	}
	if !slices.Equal(rep.completed, []string{"action.step.a"}) || !slices.Equal(rep.undos, []string{"action.step.a"}) {
		t.Errorf("a should complete with its undo: completed %v, undos %v", rep.completed, rep.undos)
	}
	if slices.Contains(rep.events, "start:action.step.c") {
		t.Errorf("c should never start: %v", rep.events)
	}
	if len(rep.runs) != 1 || rep.runs[0].OK || rep.runs[0].Failed != 1 || rep.runs[0].Succeeded != 1 {
		t.Errorf("want one failed RunCompleted, got %+v", rep.runs)
	}
}

// TestTeardownFollowsCompletionOrder: after b completed before a,
// teardown undoes a first, then b, even though g.Order is a, b.
func TestTeardownFollowsCompletionOrder(t *testing.T) {
	var calls []string
	reg := provider.NewRegistry()
	if err := reg.Register("hook", hookProvider{fn: func(_ context.Context, v string) error {
		calls = append(calls, v)
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.step.a": {ID: "action.step.a", Kind: graph.ActionNode, ActionType: "step"},
			"action.step.b": {ID: "action.step.b", Kind: graph.ActionNode, ActionType: "step"},
		},
		Order: []graph.NodeID{"action.step.a", "action.step.b"},
	}
	ps := &parser.ParsedScenario{Scenario: &parser.ScenarioBlock{Name: "s"}}
	undo := func(v string) provider.Invocation {
		return provider.Invocation{Action: "step", Attrs: map[string]cty.Value{"value": cty.StringVal(v)}}
	}
	undos := map[string]provider.Invocation{"action.step.a": undo("a"), "action.step.b": undo("b")}
	if _, err := eng.Teardown(context.Background(), ps, g, nil, nil, undos, nil,
		[]string{"action.step.b", "action.step.a"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"a", "b"}) {
		t.Fatalf("want undos in reverse completion order [a b], got %v", calls)
	}
}

package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// sleeperProvider sleeps for a fixed duration inside Execute,
// respecting context cancellation. Used to verify the engine
// applies the action's Timeout correctly.
type sleeperProvider struct {
	sleep time.Duration
}

func (sleeperProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "sleeper", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"wait": {Attrs: map[string]protocol.AttrSchema{}},
		},
	}
}
func (sleeperProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (p sleeperProvider) Execute(ctx context.Context, _ string, _ map[string]cty.Value) (map[string]cty.Value, error) {
	select {
	case <-time.After(p.sleep):
		return map[string]cty.Value{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (sleeperProvider) Close() error { return nil }

func TestTimeoutFiresOnSlowAction(t *testing.T) {
	reg := provider.NewRegistry()
	if err := reg.Register("sleeper", sleeperProvider{sleep: 500 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}

	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.wait.x": {
				ID:         "action.wait.x",
				Kind:       graph.ActionNode,
				ActionType: "wait",
				Timeout:    50 * time.Millisecond,
			},
		},
		Order: []graph.NodeID{"action.wait.x"},
	}
	ps := &parser.ParsedScenario{
		Scenario:  &parser.ScenarioBlock{Name: "t"},
		Variables: map[string]cty.Value{},
	}

	start := time.Now()
	_, err = eng.Execute(context.Background(), ps, g)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrapped DeadlineExceeded, got %v", err)
	}
	if !strings.Contains(err.Error(), "action.wait.x") {
		t.Errorf("error should name the action: %v", err)
	}
	if !strings.Contains(err.Error(), "50ms") {
		t.Errorf("error should include the configured timeout: %v", err)
	}
	// The provider was asked to sleep 500ms; with a 50ms deadline
	// we should return well before the provider's own timer.
	if elapsed > 300*time.Millisecond {
		t.Errorf("engine took %s — timeout didn't fire promptly", elapsed)
	}
}

func TestTimeoutNotAppliedWhenZero(t *testing.T) {
	// A fast provider with Timeout=0 should complete normally —
	// the engine must NOT apply a zero-duration deadline.
	reg := provider.NewRegistry()
	if err := reg.Register("sleeper", sleeperProvider{sleep: 10 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}

	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.wait.x": {
				ID:         "action.wait.x",
				Kind:       graph.ActionNode,
				ActionType: "wait",
				// Timeout omitted — defaults to zero.
			},
		},
		Order: []graph.NodeID{"action.wait.x"},
	}
	ps := &parser.ParsedScenario{
		Scenario:  &parser.ScenarioBlock{Name: "t"},
		Variables: map[string]cty.Value{},
	}
	if _, err := eng.Execute(context.Background(), ps, g); err != nil {
		t.Fatalf("zero-timeout action should not error: %v", err)
	}
}

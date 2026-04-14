package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// flakyProvider fails a configurable number of times before
// succeeding. calls is incremented on every Execute to let tests
// assert exactly how many attempts ran.
type flakyProvider struct {
	calls        atomic.Int32
	failUntil    int32 // return an error while calls <= this value
	errOnFailure error
}

func (flakyProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "flaky", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"try": {Attrs: map[string]protocol.AttrSchema{}},
		},
	}
}
func (flakyProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (p *flakyProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	n := p.calls.Add(1)
	if n <= p.failUntil {
		return nil, p.errOnFailure
	}
	return map[string]cty.Value{"attempt": cty.NumberIntVal(int64(n))}, nil
}
func (flakyProvider) Close() error { return nil }

func runWithRetry(t *testing.T, p *flakyProvider, cfg *graph.RetryConfig) (map[string]cty.Value, error) {
	t.Helper()
	reg := provider.NewRegistry()
	if err := reg.Register("flaky", p); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.try.x": {
				ID:         "action.try.x",
				Kind:       graph.ActionNode,
				ActionType: "try",
				Retry:      cfg,
			},
		},
		Order: []graph.NodeID{"action.try.x"},
	}
	ps := &parser.ParsedScenario{
		Scenario:  &parser.ScenarioBlock{Name: "t"},
		Variables: map[string]cty.Value{},
	}
	result, err := eng.Execute(context.Background(), ps, g)
	if err != nil {
		return nil, err
	}
	return result.Outputs, nil
}

func TestRetrySucceedsOnLaterAttempt(t *testing.T) {
	p := &flakyProvider{failUntil: 2, errOnFailure: errors.New("transient")}
	if _, err := runWithRetry(t, p, &graph.RetryConfig{
		MaxAttempts: 5,
		Delay:       1 * time.Millisecond,
		Backoff:     graph.BackoffConstant,
	}); err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if got := p.calls.Load(); got != 3 {
		t.Errorf("calls: got %d want 3", got)
	}
}

func TestRetryExhaustsAttempts(t *testing.T) {
	p := &flakyProvider{failUntil: 100, errOnFailure: errors.New("always fails")}
	_, err := runWithRetry(t, p, &graph.RetryConfig{
		MaxAttempts: 3,
		Delay:       1 * time.Millisecond,
		Backoff:     graph.BackoffConstant,
	})
	if err == nil {
		t.Fatal("expected exhaustion error")
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Errorf("error should name attempt count: %v", err)
	}
	if !strings.Contains(err.Error(), "always fails") {
		t.Errorf("error should wrap the last provider error: %v", err)
	}
	if got := p.calls.Load(); got != 3 {
		t.Errorf("calls: got %d want 3", got)
	}
}

func TestRetryRespectsCtxCancellation(t *testing.T) {
	p := &flakyProvider{failUntil: 100, errOnFailure: errors.New("always fails")}

	// Long delay between attempts + quick ctx cancel — we should
	// exit between attempts without burning through all of them.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	reg := provider.NewRegistry()
	if err := reg.Register("flaky", p); err != nil {
		t.Fatal(err)
	}
	eng, _ := New(reg)
	g := &graph.Graph{
		Nodes: map[graph.NodeID]*graph.Node{
			"action.try.x": {
				ID:         "action.try.x",
				Kind:       graph.ActionNode,
				ActionType: "try",
				Retry: &graph.RetryConfig{
					MaxAttempts: 10,
					Delay:       200 * time.Millisecond,
					Backoff:     graph.BackoffConstant,
				},
			},
		},
		Order: []graph.NodeID{"action.try.x"},
	}
	ps := &parser.ParsedScenario{
		Scenario:  &parser.ScenarioBlock{Name: "t"},
		Variables: map[string]cty.Value{},
	}

	start := time.Now()
	_, err := eng.Execute(ctx, ps, g)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected ctx-cancel error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	// One attempt ran; the second sleep was cancelled.
	if got := p.calls.Load(); got != 1 {
		t.Errorf("calls: got %d want 1 (cancellation before second attempt)", got)
	}
	// Should exit well before the 200ms between-attempt sleep
	// plus the next attempt would have taken.
	if elapsed > 150*time.Millisecond {
		t.Errorf("took too long to cancel: %s", elapsed)
	}
}

func TestComputeRetryDelayStrategies(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *graph.RetryConfig
		attempt int
		want    time.Duration
	}{
		{
			name:    "constant",
			cfg:     &graph.RetryConfig{Delay: 100 * time.Millisecond, Backoff: graph.BackoffConstant},
			attempt: 3,
			want:    100 * time.Millisecond,
		},
		{
			name:    "linear at attempt 1",
			cfg:     &graph.RetryConfig{Delay: 100 * time.Millisecond, Backoff: graph.BackoffLinear},
			attempt: 1,
			want:    100 * time.Millisecond,
		},
		{
			name:    "linear at attempt 3",
			cfg:     &graph.RetryConfig{Delay: 100 * time.Millisecond, Backoff: graph.BackoffLinear},
			attempt: 3,
			want:    300 * time.Millisecond,
		},
		{
			name:    "exponential at attempt 1",
			cfg:     &graph.RetryConfig{Delay: 100 * time.Millisecond, Backoff: graph.BackoffExponential},
			attempt: 1,
			want:    100 * time.Millisecond,
		},
		{
			name:    "exponential at attempt 4",
			cfg:     &graph.RetryConfig{Delay: 100 * time.Millisecond, Backoff: graph.BackoffExponential},
			attempt: 4,
			want:    800 * time.Millisecond,
		},
		{
			name:    "capped at MaxDelay",
			cfg:     &graph.RetryConfig{Delay: 100 * time.Millisecond, Backoff: graph.BackoffExponential, MaxDelay: 300 * time.Millisecond},
			attempt: 5,
			want:    300 * time.Millisecond,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeRetryDelay(c.cfg, c.attempt)
			if got != c.want {
				t.Errorf("got %s want %s", got, c.want)
			}
		})
	}
}

// Silence unused in this build variation.
var _ = fmt.Sprintf

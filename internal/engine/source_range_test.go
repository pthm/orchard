package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// boomProvider executes one "boom" action that always returns an
// error. Used as a lightweight vehicle for runtime-error tests that
// don't need any real side effects.
type boomProvider struct{}

func (boomProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "boom", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"boom": {Attrs: map[string]protocol.AttrSchema{}},
		},
	}
}
func (boomProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (boomProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, errors.New("synthetic boom")
}
func (boomProvider) Close() error { return nil }

// TestExecuteError_IncludesSourceLocation: when a provider's Execute
// returns an error, the wrapped error from Engine.Execute names the
// scenario file and the line the offending action block starts on.
// This is what makes runtime failures point at a specific author
// location instead of a bare node id.
func TestExecuteError_IncludesSourceLocation(t *testing.T) {
	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "failing.hcl")
	if err := os.WriteFile(scenarioPath, []byte(`
scenario "s" {
  required_providers {
    boom = { source = "builtin/boom" }
  }
  provider "boom" {}

  action "boom" "explode" {
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := parser.LoadScenario(scenarioPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(ps)
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	if err := reg.Register("boom", boomProvider{}); err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(reg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Execute(context.Background(), ps, g)
	if err == nil {
		t.Fatal("expected execution error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "failing.hcl:") {
		t.Errorf("error should name the scenario file: %v", err)
	}
	if !strings.Contains(msg, "action.boom.explode") {
		t.Errorf("error should name the failing node id: %v", err)
	}
	if !strings.Contains(msg, "synthetic boom") {
		t.Errorf("error should wrap the provider's error: %v", err)
	}
}

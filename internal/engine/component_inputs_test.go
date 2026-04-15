package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// sinkProvider satisfies provider.Provider with an action whose
// body accepts a single required string attribute and returns it
// as an output. Just enough to run a component's internal action
// through the engine during these tests.
type sinkProvider struct{}

func (sinkProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: "sink", Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"record": {Attrs: map[string]protocol.AttrSchema{
				"value": {Type: "string", Required: true},
			}},
		},
	}
}
func (sinkProvider) Configure(context.Context, map[string]cty.Value) error { return nil }
func (sinkProvider) Execute(_ context.Context, _ string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	return map[string]cty.Value{"echoed": attrs["value"]}, nil
}
func (sinkProvider) Close() error { return nil }

func writeScenario(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func runWithSink(t *testing.T, scenarioPath string, vars map[string]string) (map[string]cty.Value, error) {
	t.Helper()
	ps, err := parser.LoadScenario(scenarioPath, "", vars)
	if err != nil {
		return nil, err
	}
	g, err := graph.Build(ps)
	if err != nil {
		return nil, err
	}
	reg := provider.NewRegistry()
	if err := reg.Register("sink", sinkProvider{}); err != nil {
		return nil, err
	}
	eng, err := New(reg)
	if err != nil {
		return nil, err
	}
	result, err := eng.Execute(context.Background(), ps, g)
	if err != nil {
		return nil, err
	}
	return result.Outputs, nil
}

func TestComponentInputRejectsUndeclared(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "comp.hcl", `
component "c" {
  variable "name" {
    type    = string
    default = "x"
  }
  action "record" "inner" { value = var.name }
  output "echoed" { value = action.record.inner.echoed }
}`)
	scenario := writeScenario(t, dir, "scenario.hcl", `
scenario "s" {
  component "c" "one" {
    source = "./comp.hcl"
    inputs = {
      name  = "provided"
      extra = "not declared"
    }
  }
}`)

	_, err := runWithSink(t, scenario, nil)
	if err == nil {
		t.Fatal("expected undeclared-input error")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("error should name the offending input: %v", err)
	}
}

func TestComponentInputRejectsMissingRequired(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "comp.hcl", `
component "c" {
  variable "name" { type = string }
  action "record" "inner" { value = var.name }
  output "echoed" { value = action.record.inner.echoed }
}`)
	scenario := writeScenario(t, dir, "scenario.hcl", `
scenario "s" {
  component "c" "one" {
    source = "./comp.hcl"
  }
}`)

	_, err := runWithSink(t, scenario, nil)
	if err == nil {
		t.Fatal("expected required-variable error")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should name the missing variable: %v", err)
	}
}

func TestComponentInputTypeMismatch(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "comp.hcl", `
component "c" {
  variable "count" { type = number }
  action "record" "inner" { value = "${var.count}" }
  output "echoed" { value = action.record.inner.echoed }
}`)
	scenario := writeScenario(t, dir, "scenario.hcl", `
scenario "s" {
  component "c" "one" {
    source = "./comp.hcl"
    inputs = {
      count = "not a number"
    }
  }
}`)

	_, err := runWithSink(t, scenario, nil)
	if err == nil {
		t.Fatal("expected type-mismatch error")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Errorf("error should name the input: %v", err)
	}
}

func TestComponentInputHappyPath(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "comp.hcl", `
component "c" {
  variable "label" { type = string }
  action "record" "inner" { value = var.label }
  output "echoed" { value = action.record.inner.echoed }
}`)
	scenario := writeScenario(t, dir, "scenario.hcl", `
scenario "s" {
  component "c" "one" {
    source = "./comp.hcl"
    inputs = {
      label = "hello"
    }
  }
  output "out" { value = component.c.one.echoed }
}`)

	outputs, err := runWithSink(t, scenario, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := outputs["out"].AsString(); got != "hello" {
		t.Fatalf("output: got %q want %q", got, "hello")
	}
}

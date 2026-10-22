package engine

import (
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
)

func TestCountAndForEach(t *testing.T) {
	dir := t.TempDir()
	writeScenario(t, dir, "comp.hcl", `
component "c" {
  variable "name" { type = string }
  action "record" "inner" { value = var.name }
  output "echoed" { value = action.record.inner.echoed }
}`)
	scenario := writeScenario(t, dir, "scenario.hcl", `
scenario "s" {
  variable "n" { default = 11 }

  action "record" "item" {
    count = var.n
    value = "item-${count.index}"
  }

  component "c" "site" {
    source   = "./comp.hcl"
    for_each = { eu = "Europe", us = "America" }
    inputs   = { name = "${each.key}:${each.value}" }
  }

  action "record" "summary" {
    value = join(",", action.record.item[*].echoed)
  }

  output "summary" { value = action.record.summary.echoed }
  output "eu"      { value = component.c.site["eu"].echoed }
  output "tenth"   { value = action.record.item[10].echoed }
}`)

	ps, err := parser.LoadScenario(scenario, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.Build(ps)
	if err != nil {
		t.Fatal(err)
	}
	// Instances run in index order, and the summary waits on all of them.
	order := strings.Join(func() []string {
		var ids []string
		for _, id := range g.Order {
			if strings.HasPrefix(string(id), "action.record.") {
				ids = append(ids, string(id))
			}
		}
		return ids
	}(), " ")
	if !strings.Contains(order, "item[2] action.record.item[3]") || !strings.HasSuffix(order, "item[10] action.record.summary") {
		t.Errorf("unexpected order: %s", order)
	}

	reg := provider.NewRegistry()
	if err := reg.Register("sink", sinkProvider{}); err != nil {
		t.Fatal(err)
	}
	eng, err := New(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.ValidateReferences(ps, g); err != nil {
		t.Fatalf("plan-time validation: %v", err)
	}

	outputs, err := runWithSink(t, scenario, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"summary": "item-0,item-1,item-2,item-3,item-4,item-5,item-6,item-7,item-8,item-9,item-10",
		"eu":      "eu:Europe",
		"tenth":   "item-10",
	}
	for k, v := range want {
		if got := outputs[k].AsString(); got != v {
			t.Errorf("output %s: got %q want %q", k, got, v)
		}
	}
}

func TestRepetitionErrors(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"count from action output": {`
  action "record" "a" { value = "x" }
  action "record" "b" {
    count = length(action.record.a.echoed)
    value = "y"
  }`, "may only reference var.*"},
		"both set": {`
  action "record" "a" {
    count    = 1
    for_each = ["x"]
    value    = "x"
  }`, "mutually exclusive"},
		"self reference": {`
  action "record" "a" {
    count = 2
    value = action.record.a[0].echoed
  }`, "references its own"},
		"duplicate key": {`
  action "record" "a" {
    for_each = ["x", "x"]
    value    = each.key
  }`, "duplicate key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeScenario(t, t.TempDir(), "s.hcl", "scenario \"s\" {"+tc.body+"\n}")
			_, err := runWithSink(t, path, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

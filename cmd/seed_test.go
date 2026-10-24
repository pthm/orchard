package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/record"
)

// seededScenario calls uuid() in every place an expression is
// evaluated: a variable default, count, an action attribute, a
// component's input, default, action and output, and scenario outputs.
func seededScenario(t *testing.T, seedAttr string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "comp.hcl"), `component "c" {
  variable "x" { type = string }
  variable "d" { default = uuid() }
  action "exec_run" "b" {
    command = "echo"
    args    = [var.x, var.d, uuid()]
  }
  output "y" { value = "${action.exec_run.b.stdout}${uuid()}" }
}
`)
	path := filepath.Join(dir, "seeded.hcl")
	writeFile(t, path, `scenario "seeded" {
  `+seedAttr+`
  required_providers {
    sh = { source = "builtin/exec" }
  }
  variable "tag" { default = uuid() }
  component "c" "one" {
    source = "./comp.hcl"
    inputs = { x = uuid() }
  }
  action "exec_run" "a" {
    count   = length(split("-", uuid())) - 3
    command = "echo"
    args    = [uuid(), var.tag, uuid()]
  }
  action "exec_run" "z" {
    command = "echo"
    args    = [uuid()]
  }
  output "o" { value = uuid() }
  output "a" { value = action.exec_run.a[*].stdout }
  output "c" { value = component.c.one.y }
}
`)
	return path
}

// seededValues runs scenario and returns everything random the record
// holds: variables, outputs, and each node's outputs.
func seededValues(t *testing.T, scenario string, args ...string) (string, *record.Record) {
	t.Helper()
	rec, err := record.Load(runRecorded(t, scenario, args...))
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]map[string]json.RawMessage{}
	for _, n := range rec.Nodes {
		nodes[n.ID] = n.Outputs
	}
	b, _ := json.Marshal(map[string]any{"vars": rec.Variables, "outputs": rec.Outputs, "nodes": nodes})
	return string(b), rec
}

func TestSeedReproducesRun(t *testing.T) {
	scenario := seededScenario(t, "")
	first, rec := seededValues(t, scenario, "--seed", "7")
	if rec.Seed == nil || *rec.Seed != 7 {
		t.Fatalf("record seed = %v, want 7", rec.Seed)
	}
	if !strings.Contains(first, "exec_run.a[1]") {
		t.Fatalf("count should expand to 2 instances: %s", first)
	}
	if again, _ := seededValues(t, scenario, "--seed", "7", "--parallelism", "4"); again != first {
		t.Errorf("same seed, different values:\n%s\n%s", first, again)
	}
	if other, _ := seededValues(t, scenario, "--seed", "8"); other == first {
		t.Error("a different seed gave the same values")
	}
	unseeded, rec := seededValues(t, scenario)
	if rec.Seed == nil {
		t.Fatal("an unseeded run should record the seed it picked")
	}
	if replay, _ := seededValues(t, scenario, "--seed", jsonNum(*rec.Seed)); replay != unseeded {
		t.Errorf("replaying the recorded seed gave different values:\n%s\n%s", unseeded, replay)
	}
}

func TestScenarioSeedAttribute(t *testing.T) {
	flag, _ := seededValues(t, seededScenario(t, ""), "--seed", "7")
	pinned := seededScenario(t, "seed = 7")
	if attr, _ := seededValues(t, pinned); attr != flag {
		t.Errorf("seed = 7 and --seed 7 differ:\n%s\n%s", attr, flag)
	}
	if over, _ := seededValues(t, pinned, "--seed", "8"); over == flag {
		t.Error("--seed should override the scenario's seed")
	}
}

// TestPlanShowsRunValues: plan --show-values with a seed shows the
// values the run with that seed uses.
func TestPlanShowsRunValues(t *testing.T) {
	scenario := seededScenario(t, "")
	out, err := runCLI(t, "plan", scenario, "--no-color", "--show-values", "--seed", "7")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "seed: 7") {
		t.Errorf("plan should print the seed:\n%s", out)
	}
	_, rec := seededValues(t, scenario, "--seed", "7")
	for _, n := range rec.Nodes {
		if n.ID != "action.exec_run.z" {
			continue
		}
		var stdout string
		_ = json.Unmarshal(n.Outputs["stdout"], &stdout)
		if id := strings.TrimSpace(stdout); id == "" || !strings.Contains(out, id) {
			t.Errorf("plan doesn't show the run's value %q:\n%s", id, out)
		}
	}
}

func jsonNum(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

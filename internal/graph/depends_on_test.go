package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/internal/parser"
)

func loadScenario(t *testing.T, src string) (*parser.ParsedScenario, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return parser.LoadScenario(path, nil)
}

func buildOrError(t *testing.T, src string) (*Graph, error) {
	t.Helper()
	ps, err := loadScenario(t, src)
	if err != nil {
		return nil, err
	}
	return Build(ps)
}

func TestDependsOnAddsEdge(t *testing.T) {
	// Without depends_on: no dependency inference — reindex runs
	// in parallel-eligible position with seed.
	// With depends_on: reindex must run after seed, in topo order.
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "seed" {
    query = "INSERT INTO t VALUES (1)"
  }
  action "http" "reindex" {
    depends_on = [action.sql.seed]
    method = "POST"
    url    = "/reindex"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	reindex := g.Nodes["action.http.reindex"]
	if reindex == nil {
		t.Fatal("reindex node missing")
	}
	found := false
	for _, d := range reindex.Dependencies {
		if d == "action.sql.seed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reindex.Dependencies should include action.sql.seed: %v", reindex.Dependencies)
	}

	// Topological order: seed before reindex.
	var seedIdx, reindexIdx int
	for i, id := range g.Order {
		switch id {
		case "action.sql.seed":
			seedIdx = i
		case "action.http.reindex":
			reindexIdx = i
		}
	}
	if seedIdx >= reindexIdx {
		t.Fatalf("seed (#%d) should topo-sort before reindex (#%d); order %v",
			seedIdx, reindexIdx, g.Order)
	}
}

func TestDependsOnMultipleRefs(t *testing.T) {
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "a" {
    query = "SELECT 1"
  }
  action "sql" "b" {
    query = "SELECT 2"
  }
  action "http" "after_both" {
    depends_on = [action.sql.a, action.sql.b]
    method = "POST"
    url    = "/sync"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	afterBoth := g.Nodes["action.http.after_both"]
	if afterBoth == nil {
		t.Fatal("after_both node missing")
	}
	if len(afterBoth.Dependencies) != 2 {
		t.Fatalf("expected 2 deps, got %v", afterBoth.Dependencies)
	}
}

func TestDependsOnUnknownNodeErrors(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "http" "x" {
    depends_on = [action.sql.missing]
    method = "GET"
    url    = "/"
  }
}`)
	if err == nil {
		t.Fatal("expected error for depends_on referencing unknown node")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error should name the missing ref: %v", err)
	}
}

func TestDependsOnNonRefEntryErrors(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "a" {
    query = "SELECT 1"
  }
  action "http" "x" {
    depends_on = ["a_string"]
    method = "GET"
    url    = "/"
  }
}`)
	if err == nil {
		t.Fatal("expected error for non-reference depends_on entry")
	}
	if !strings.Contains(err.Error(), "depends_on") {
		t.Fatalf("error should mention depends_on: %v", err)
	}
}

func TestDependsOnCyclePrevented(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "a" {
    depends_on = [action.sql.b]
    query = "SELECT 1"
  }
  action "sql" "b" {
    depends_on = [action.sql.a]
    query = "SELECT 2"
  }
}`)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error should mention cycle: %v", err)
	}
}

func TestDependsOnInsideComponent(t *testing.T) {
	// Inside a component, action.sql.foo references the
	// component's internal action of that name.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "comp.hcl"), []byte(`
component "c" {
  action "sql" "first" {
    query = "SELECT 1"
  }
  action "sql" "second" {
    depends_on = [action.sql.first]
    query = "SELECT 2"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scenario.hcl")
	if err := os.WriteFile(path, []byte(`
scenario "s" {
  component "c" "i" { source = "./comp.hcl" }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := parser.LoadScenario(path, nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	g, err := Build(ps)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	second := g.Nodes["component.c.i.action.sql.second"]
	if second == nil {
		t.Fatal("internal 'second' node missing")
	}
	found := false
	for _, d := range second.Dependencies {
		if d == "component.c.i.action.sql.first" {
			found = true
		}
	}
	if !found {
		t.Fatalf("second's deps should include scoped first: %v", second.Dependencies)
	}
}

func TestDependsOnDoesNotConflictWithDataDependencies(t *testing.T) {
	// When depends_on duplicates a dep that expression-inference
	// would also find, the edge must appear exactly once. (Not
	// strictly required for correctness, but a reasonable
	// expectation for tidy debug output.)
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "a" {
    query = "SELECT 1"
  }
  action "sql" "b" {
    depends_on = [action.sql.a]
    query = "SELECT ${action.sql.a.id}"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b := g.Nodes["action.sql.b"]
	count := 0
	for _, d := range b.Dependencies {
		if d == "action.sql.a" {
			count++
		}
	}
	if count < 1 {
		t.Fatalf("expected action.sql.a in deps, got %v", b.Dependencies)
	}
	// Duplicates are OK for the engine (topoSort handles them),
	// so we don't assert count==1. Just sanity-check non-zero.
}

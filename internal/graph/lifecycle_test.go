package graph

import (
	"strings"
	"testing"
)

func TestManagedTeardownFlag(t *testing.T) {
	g, err := buildOrError(t, `
scenario "s" {
  action "pg_insert" "default" {
    table = "t"
  }
  action "pg_insert" "kept" {
    table = "t"
    lifecycle {
      managed_teardown = false
    }
  }
  action "pg_insert" "explicit_on" {
    table = "t"
    lifecycle {
      managed_teardown = true
      teardown {
        table = "log"
      }
    }
  }
}
`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Nodes["action.pg_insert.default"].ManagedTeardown != nil {
		t.Error("managed_teardown should be unset by default")
	}
	if m := g.Nodes["action.pg_insert.kept"].ManagedTeardown; m == nil || *m {
		t.Error("managed_teardown = false should opt out")
	}
	n := g.Nodes["action.pg_insert.explicit_on"]
	if n.ManagedTeardown == nil || !*n.ManagedTeardown || n.Teardown == nil {
		t.Errorf("managed_teardown = true alongside a teardown block: %+v", n)
	}
}

func TestManagedTeardownMustBeBool(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "pg_insert" "x" {
    table = "t"
    lifecycle {
      managed_teardown = "no"
    }
  }
}
`)
	if err == nil || !strings.Contains(err.Error(), "managed_teardown must be true or false") {
		t.Fatalf("expected a bool error, got %v", err)
	}
}

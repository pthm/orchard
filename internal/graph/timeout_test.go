package graph

import (
	"strings"
	"testing"
	"time"
)

func TestTimeoutParsedOnNode(t *testing.T) {
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "slow" {
    timeout = "5s"
    query   = "SELECT pg_sleep(10)"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n := g.Nodes["action.sql.slow"]
	if n == nil {
		t.Fatal("slow node missing")
	}
	if n.Timeout != 5*time.Second {
		t.Fatalf("Timeout: got %s want 5s", n.Timeout)
	}
}

func TestTimeoutAbsentZero(t *testing.T) {
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "fast" {
    query = "SELECT 1"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if n := g.Nodes["action.sql.fast"]; n.Timeout != 0 {
		t.Fatalf("absent timeout should be zero, got %s", n.Timeout)
	}
}

func TestTimeoutMalformedStringErrors(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    timeout = "not a duration"
    query   = "SELECT 1"
  }
}`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error should mention timeout: %v", err)
	}
}

func TestTimeoutNonStringErrors(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    timeout = 30
    query   = "SELECT 1"
  }
}`)
	if err == nil {
		t.Fatal("expected type error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error should mention timeout: %v", err)
	}
}

func TestTimeoutNegativeRejected(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    timeout = "-5s"
    query   = "SELECT 1"
  }
}`)
	if err == nil {
		t.Fatal("expected negative-timeout error")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Fatalf("error should mention positivity: %v", err)
	}
}

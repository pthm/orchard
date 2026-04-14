package graph

import (
	"strings"
	"testing"
	"time"
)

func TestRetryParseHappyPath(t *testing.T) {
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "flaky" {
    retry {
      max_attempts = 5
      delay        = "200ms"
      backoff      = "linear"
      max_delay    = "5s"
    }
    query = "SELECT 1"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n := g.Nodes["action.sql.flaky"]
	if n.Retry == nil {
		t.Fatal("Retry should be non-nil")
	}
	if n.Retry.MaxAttempts != 5 {
		t.Errorf("MaxAttempts: got %d", n.Retry.MaxAttempts)
	}
	if n.Retry.Delay != 200*time.Millisecond {
		t.Errorf("Delay: got %s", n.Retry.Delay)
	}
	if n.Retry.Backoff != BackoffLinear {
		t.Errorf("Backoff: got %d", n.Retry.Backoff)
	}
	if n.Retry.MaxDelay != 5*time.Second {
		t.Errorf("MaxDelay: got %s", n.Retry.MaxDelay)
	}
}

func TestRetryDefaults(t *testing.T) {
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    retry {
      max_attempts = 3
    }
    query = "SELECT 1"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cfg := g.Nodes["action.sql.x"].Retry
	if cfg == nil {
		t.Fatal("Retry should be non-nil")
	}
	if cfg.Delay != time.Second {
		t.Errorf("Delay default: got %s want 1s", cfg.Delay)
	}
	if cfg.Backoff != BackoffExponential {
		t.Errorf("Backoff default: got %d want Exponential", cfg.Backoff)
	}
	if cfg.MaxDelay != 0 {
		t.Errorf("MaxDelay: got %s want zero", cfg.MaxDelay)
	}
}

func TestRetryAbsentIsNil(t *testing.T) {
	g, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    query = "SELECT 1"
  }
}`)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Nodes["action.sql.x"].Retry != nil {
		t.Error("Retry should be nil when block absent")
	}
}

func TestRetryMissingMaxAttemptsErrors(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    retry {
      delay = "1s"
    }
    query = "SELECT 1"
  }
}`)
	if err == nil {
		t.Fatal("expected error for missing max_attempts")
	}
	if !strings.Contains(err.Error(), "max_attempts") {
		t.Fatalf("error should mention max_attempts: %v", err)
	}
}

func TestRetryZeroAttemptsRejected(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    retry {
      max_attempts = 0
    }
    query = "SELECT 1"
  }
}`)
	if err == nil {
		t.Fatal("expected error for max_attempts = 0")
	}
	if !strings.Contains(err.Error(), "at least 1") {
		t.Fatalf("error should mention the minimum: %v", err)
	}
}

func TestRetryBadBackoffRejected(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    retry {
      max_attempts = 2
      backoff      = "mystery"
    }
    query = "SELECT 1"
  }
}`)
	if err == nil {
		t.Fatal("expected error for unknown backoff")
	}
	if !strings.Contains(err.Error(), "backoff") {
		t.Fatalf("error should mention backoff: %v", err)
	}
}

func TestRetryMultipleBlocksRejected(t *testing.T) {
	_, err := buildOrError(t, `
scenario "s" {
  action "sql" "x" {
    retry {
      max_attempts = 2
    }
    retry {
      max_attempts = 3
    }
    query = "SELECT 1"
  }
}`)
	if err == nil {
		t.Fatal("expected error for multiple retry blocks")
	}
	if !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("error should mention the one-block rule: %v", err)
	}
}

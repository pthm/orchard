package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// The happy-path execution tests for the postgres provider require a
// live database and live as integration tests (to be added in a
// follow-up change). The tests here exercise the wiring that does
// not need a connection: schema loading, compile-time interface
// conformance, Configure validation, and dispatch. The value
// converter is shared and tested in internal/sqlrow.

func TestSchemaLoadsFromEmbeddedHCL(t *testing.T) {
	p := New()
	s := p.Schema()
	if s.Name != "postgres" {
		t.Fatalf("name: got %q want postgres", s.Name)
	}
	if s.Version != "1" || s.Protocol != "1" {
		t.Fatalf("version/protocol: %q/%q", s.Version, s.Protocol)
	}
	if _, ok := s.Config["dsn"]; !ok {
		t.Fatal("schema missing config.dsn")
	}
	if s.Config["dsn"].Type != "string" || !s.Config["dsn"].Required {
		t.Fatalf("config.dsn: %+v", s.Config["dsn"])
	}
	if _, ok := s.Actions["postgres_query"]; !ok {
		t.Fatal("schema missing action.postgres_query")
	}
	if s.Actions["postgres_query"].Attrs["query"].Type != "string" {
		t.Fatalf("action.postgres_query.query type: %q", s.Actions["postgres_query"].Attrs["query"].Type)
	}
}

func TestConfigureRequiresDSN(t *testing.T) {
	p := New()
	err := p.Configure(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "dsn is required") {
		t.Fatalf("expected dsn-required error, got %v", err)
	}

	err = p.Configure(context.Background(), map[string]cty.Value{
		"dsn": cty.NullVal(cty.String),
	})
	if err == nil || !strings.Contains(err.Error(), "dsn is required") {
		t.Fatalf("null dsn: expected error, got %v", err)
	}

	err = p.Configure(context.Background(), map[string]cty.Value{
		"dsn": cty.NumberIntVal(5),
	})
	if err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Fatalf("wrong-type dsn: expected string-type error, got %v", err)
	}
}

func TestExecuteBeforeConfigure(t *testing.T) {
	p := New()
	_, err := p.Execute(context.Background(), "postgres_query", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured error, got %v", err)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	p := New()
	// Pretend Configure ran by giving it a bogus DSN — Configure does
	// not dial, so this sets p.pool to a non-nil pool object. That
	// lets us reach the action-dispatch switch.
	if err := p.Configure(context.Background(), map[string]cty.Value{
		"dsn": cty.StringVal("postgres://example:5432/db"),
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	defer p.Close()

	_, err := p.Execute(context.Background(), "imaginary", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), `unknown action "imaginary"`) {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	p := New()
	// Close on an unconfigured provider must be a no-op.
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

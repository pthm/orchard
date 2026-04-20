package postgres

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// The happy-path execution tests for the postgres provider require a
// live database and live as integration tests (to be added in a
// follow-up change). The tests here exercise the wiring that does
// not need a connection: schema loading, compile-time interface
// conformance, Configure validation, dispatch, and the pgx→cty
// value converter.

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

func TestToCtyValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want cty.Value
	}{
		{"nil", nil, cty.NullVal(cty.DynamicPseudoType)},
		{"string", "hello", cty.StringVal("hello")},
		{"bool", true, cty.BoolVal(true)},
		{"int32", int32(7), cty.NumberIntVal(7)},
		{"int64", int64(42), cty.NumberIntVal(42)},
		{"float64", float64(3.14), cty.NumberFloatVal(3.14)},
		{"big.Float", big.NewFloat(2.5), cty.NumberFloatVal(2.5)},
		{"[]byte", []byte("raw"), cty.StringVal("raw")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := toCtyValue(c.in)
			if !got.RawEquals(c.want) {
				t.Fatalf("got %#v want %#v", got, c.want)
			}
		})
	}
}

func TestToCtyValueFallback(t *testing.T) {
	type unknown struct{ X int }
	got := toCtyValue(unknown{X: 5})
	if got.Type() != cty.String {
		t.Fatalf("fallback should produce string, got %s", got.Type().FriendlyName())
	}
}

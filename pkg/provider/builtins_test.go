package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
)

// resetBuiltins snapshots and restores the package-level registry
// so tests can mutate it without leaking into production state.
func resetBuiltins(t *testing.T) {
	t.Helper()
	builtinsMu.Lock()
	saved := make(map[string]BuiltinFactory, len(builtins))
	for k, v := range builtins {
		saved[k] = v
	}
	builtins = map[string]BuiltinFactory{}
	builtinsMu.Unlock()

	t.Cleanup(func() {
		builtinsMu.Lock()
		builtins = saved
		builtinsMu.Unlock()
	})
}

type builtinStub struct{ name string }

func (b *builtinStub) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: b.name, Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{"noop": {Attrs: map[string]protocol.AttrSchema{}}},
	}
}
func (b *builtinStub) Configure(context.Context, map[string]cty.Value) error { return nil }
func (b *builtinStub) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, nil
}
func (b *builtinStub) Close() error { return nil }

func TestRegisterAndNewBuiltin(t *testing.T) {
	resetBuiltins(t)

	RegisterBuiltin("builtin/stub", func() Provider { return &builtinStub{name: "stub"} })

	p, err := NewBuiltin("builtin/stub")
	if err != nil {
		t.Fatalf("NewBuiltin: %v", err)
	}
	if p.Schema().Name != "stub" {
		t.Fatalf("unexpected schema: %+v", p.Schema())
	}

	// Each factory call returns a fresh instance.
	p2, _ := NewBuiltin("builtin/stub")
	if p == p2 {
		t.Fatal("NewBuiltin should return a fresh instance per call")
	}
}

func TestNewBuiltinUnknown(t *testing.T) {
	resetBuiltins(t)
	_, err := NewBuiltin("builtin/nonexistent")
	if err == nil || !strings.Contains(err.Error(), "unknown provider source") {
		t.Fatalf("expected unknown-source error, got %v", err)
	}
}

func TestRegisterBuiltinDuplicatePanics(t *testing.T) {
	resetBuiltins(t)
	RegisterBuiltin("builtin/stub", func() Provider { return &builtinStub{name: "a"} })

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
		if !strings.Contains(r.(string), "duplicate") {
			t.Fatalf("panic message should mention duplicate, got %v", r)
		}
	}()
	RegisterBuiltin("builtin/stub", func() Provider { return &builtinStub{name: "b"} })
}

func TestBuiltinSourcesSorted(t *testing.T) {
	resetBuiltins(t)
	RegisterBuiltin("zulu", func() Provider { return &builtinStub{name: "z"} })
	RegisterBuiltin("alpha", func() Provider { return &builtinStub{name: "a"} })
	RegisterBuiltin("mike", func() Provider { return &builtinStub{name: "m"} })

	got := BuiltinSources()
	want := []string{"alpha", "mike", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("len: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d: got %q want %q", i, got[i], want[i])
		}
	}
}

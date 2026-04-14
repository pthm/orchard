package provider

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
)

// stubProvider is a minimal Provider used to exercise the Registry.
// It records the sequence of lifecycle calls so tests can assert on
// ordering without reaching for a mock framework.
type stubProvider struct {
	mu            sync.Mutex
	name          string
	configErr     error
	closeErr      error
	configured    bool
	closed        bool
	configCalls   int
	closeCalls    int
	executeCalls  int
	sawConfigKeys []string
	lifecycle     *[]string
}

func (s *stubProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: s.name, Version: "1", Protocol: protocol.ProtocolVersion,
		Actions: map[string]protocol.ActionSchema{
			"noop": {Attrs: map[string]protocol.AttrSchema{}},
		},
	}
}

func (s *stubProvider) Configure(ctx context.Context, config map[string]cty.Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configCalls++
	for k := range config {
		s.sawConfigKeys = append(s.sawConfigKeys, k)
	}
	if s.lifecycle != nil {
		*s.lifecycle = append(*s.lifecycle, "configure:"+s.name)
	}
	if s.configErr != nil {
		return s.configErr
	}
	s.configured = true
	return nil
}

func (s *stubProvider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executeCalls++
	return map[string]cty.Value{"ok": cty.True}, nil
}

func (s *stubProvider) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	s.closed = true
	if s.lifecycle != nil {
		*s.lifecycle = append(*s.lifecycle, "close:"+s.name)
	}
	return s.closeErr
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	a := &stubProvider{name: "a"}
	b := &stubProvider{name: "b"}
	if err := r.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(b); err != nil {
		t.Fatalf("register b: %v", err)
	}

	got, err := r.Get("a")
	if err != nil || got != a {
		t.Fatalf("Get(a): got %v err %v", got, err)
	}
	if _, err := r.Get("missing"); err == nil {
		t.Fatal("expected error for missing provider")
	}

	names := r.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("Names: got %v want [a b]", names)
	}
}

func TestRegistryDuplicateRegister(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubProvider{name: "x"}); err != nil {
		t.Fatal(err)
	}
	err := r.Register(&stubProvider{name: "x"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestRegistryRejectsEmptyName(t *testing.T) {
	r := NewRegistry()
	err := r.Register(&stubProvider{name: ""})
	if err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("expected empty-name error, got %v", err)
	}
}

func TestRegistryConfigureDispatches(t *testing.T) {
	r := NewRegistry()
	a := &stubProvider{name: "a"}
	b := &stubProvider{name: "b"}
	_ = r.Register(a)
	_ = r.Register(b)

	configs := map[string]map[string]cty.Value{
		"a": {"key": cty.StringVal("val")},
		// "b" intentionally missing — Configure must still be called.
	}
	if err := r.Configure(context.Background(), configs); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if a.configCalls != 1 || b.configCalls != 1 {
		t.Fatalf("config calls: a=%d b=%d", a.configCalls, b.configCalls)
	}
	if len(a.sawConfigKeys) != 1 || a.sawConfigKeys[0] != "key" {
		t.Errorf("a did not see its config: %v", a.sawConfigKeys)
	}
	if len(b.sawConfigKeys) != 0 {
		t.Errorf("b saw unexpected config: %v", b.sawConfigKeys)
	}
}

func TestRegistryConfigureOneShot(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "a"})
	if err := r.Configure(context.Background(), nil); err != nil {
		t.Fatalf("first Configure: %v", err)
	}
	if err := r.Configure(context.Background(), nil); err == nil {
		t.Fatal("expected error on second Configure")
	}
	// Register after Configure must also fail.
	if err := r.Register(&stubProvider{name: "b"}); err == nil {
		t.Fatal("expected register-after-configure error")
	}
}

func TestRegistryConfigureAbortsOnError(t *testing.T) {
	r := NewRegistry()
	a := &stubProvider{name: "a"}
	bad := &stubProvider{name: "bad", configErr: errors.New("nope")}
	c := &stubProvider{name: "c"}
	_ = r.Register(a)
	_ = r.Register(bad)
	_ = r.Register(c)

	err := r.Configure(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("expected configure error for bad, got %v", err)
	}
	// a runs before bad, c runs after — c must NOT have been configured.
	if a.configCalls != 1 {
		t.Errorf("a configured %d times, want 1", a.configCalls)
	}
	if c.configCalls != 0 {
		t.Errorf("c configured %d times, want 0 (configure should abort)", c.configCalls)
	}
}

func TestRegistryCloseReverseOrder(t *testing.T) {
	lifecycle := []string{}
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "a", lifecycle: &lifecycle})
	_ = r.Register(&stubProvider{name: "b", lifecycle: &lifecycle})
	_ = r.Register(&stubProvider{name: "c", lifecycle: &lifecycle})

	if err := r.Configure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []string{
		"configure:a", "configure:b", "configure:c",
		"close:c", "close:b", "close:a",
	}
	if len(lifecycle) != len(want) {
		t.Fatalf("lifecycle: got %v want %v", lifecycle, want)
	}
	for i := range want {
		if lifecycle[i] != want[i] {
			t.Errorf("lifecycle[%d]: got %q want %q", i, lifecycle[i], want[i])
		}
	}
}

func TestRegistryCloseCollectsErrors(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubProvider{name: "ok"})
	_ = r.Register(&stubProvider{name: "bad1", closeErr: errors.New("boom1")})
	_ = r.Register(&stubProvider{name: "bad2", closeErr: errors.New("boom2")})

	err := r.Close()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "boom1") || !strings.Contains(msg, "boom2") {
		t.Fatalf("expected both close errors in message, got %q", msg)
	}
}

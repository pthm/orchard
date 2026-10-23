package provider

import (
	"context"
	"errors"
	"path/filepath"
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
	configSchema  map[string]protocol.AttrSchema
	configErr     error
	closeErr      error
	configured    bool
	closed        bool
	configCalls   int
	closeCalls    int
	executeCalls  int
	sawConfigKeys []string
	sawConfig     map[string]cty.Value
	lifecycle     *[]string
}

func (s *stubProvider) Schema() protocol.ProviderSchema {
	return protocol.ProviderSchema{
		Name: s.name, Version: "1", Protocol: protocol.ProtocolVersion,
		Config: s.configSchema,
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
	s.sawConfig = config
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
	if err := r.Register("a", a); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register("b", b); err != nil {
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
	if err := r.Register("x", &stubProvider{name: "x"}); err != nil {
		t.Fatal(err)
	}
	err := r.Register("x", &stubProvider{name: "x"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestRegistryRejectsEmptyLocalName(t *testing.T) {
	r := NewRegistry()
	err := r.Register("", &stubProvider{name: "x"})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-name error, got %v", err)
	}
}

func TestRegistryRejectsEmptySchemaName(t *testing.T) {
	r := NewRegistry()
	err := r.Register("x", &stubProvider{name: ""})
	if err == nil || !strings.Contains(err.Error(), "empty schema name") {
		t.Fatalf("expected empty-schema-name error, got %v", err)
	}
}

// TestRegistryAliasedProvider verifies that a provider's local
// name need not match its schema name. This is the key property
// that lets scenarios declare `required_providers { db = {
// source = "builtin/postgres" } }` and have Configure reach the
// postgres instance under the key "db".
func TestRegistryAliasedProvider(t *testing.T) {
	r := NewRegistry()
	p := &stubProvider{
		name:         "a",
		configSchema: map[string]protocol.AttrSchema{"key": {Type: "string"}},
	}
	if err := r.Register("alias_one", p); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get("alias_one")
	if err != nil || got != p {
		t.Fatalf("Get(alias_one): got %v err %v", got, err)
	}
	// Schema name should not be a valid lookup key.
	if _, err := r.Get("a"); err == nil {
		t.Fatal("Get by schema name should fail when registered under alias")
	}
	// Configure under the alias.
	if err := r.Configure(context.Background(), map[string]map[string]cty.Value{
		"alias_one": {"key": cty.StringVal("hello")},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if len(p.sawConfigKeys) != 1 || p.sawConfigKeys[0] != "key" {
		t.Fatalf("provider did not see its alias-keyed config: %v", p.sawConfigKeys)
	}
}

func TestRegistryConfigureDispatches(t *testing.T) {
	r := NewRegistry()
	a := &stubProvider{
		name:         "a",
		configSchema: map[string]protocol.AttrSchema{"key": {Type: "string"}},
	}
	b := &stubProvider{name: "b"}
	_ = r.Register("a", a)
	_ = r.Register("b", b)

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
	_ = r.Register("a", &stubProvider{name: "a"})
	if err := r.Configure(context.Background(), nil); err != nil {
		t.Fatalf("first Configure: %v", err)
	}
	if err := r.Configure(context.Background(), nil); err == nil {
		t.Fatal("expected error on second Configure")
	}
	// Register after Configure must also fail.
	if err := r.Register("b", &stubProvider{name: "b"}); err == nil {
		t.Fatal("expected register-after-configure error")
	}
}

func TestRegistryConfigureAbortsOnError(t *testing.T) {
	r := NewRegistry()
	a := &stubProvider{name: "a"}
	bad := &stubProvider{name: "bad", configErr: errors.New("nope")}
	c := &stubProvider{name: "c"}
	_ = r.Register("a", a)
	_ = r.Register("bad", bad)
	_ = r.Register("c", c)

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
	_ = r.Register("a", &stubProvider{name: "a", lifecycle: &lifecycle})
	_ = r.Register("b", &stubProvider{name: "b", lifecycle: &lifecycle})
	_ = r.Register("c", &stubProvider{name: "c", lifecycle: &lifecycle})

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
	_ = r.Register("ok", &stubProvider{name: "ok"})
	_ = r.Register("bad1", &stubProvider{name: "bad1", closeErr: errors.New("boom1")})
	_ = r.Register("bad2", &stubProvider{name: "bad2", closeErr: errors.New("boom2")})

	err := r.Close()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "boom1") || !strings.Contains(msg, "boom2") {
		t.Fatalf("expected both close errors in message, got %q", msg)
	}
}

// TestValidateConfigsRejectsUnknownAttr: a typo in a provider config
// block (dsn vs dsnn) now surfaces at plan time instead of being
// silently handed to the provider's Configure.
func TestValidateConfigsRejectsUnknownAttr(t *testing.T) {
	r := NewRegistry()
	p := &stubProvider{
		name:         "db",
		configSchema: map[string]protocol.AttrSchema{"dsn": {Type: "string", Required: true}},
	}
	_ = r.Register("db", p)

	err := r.ValidateConfigs(map[string]map[string]cty.Value{
		"db": {"dsnn": cty.StringVal("postgres://...")},
	})
	if err == nil {
		t.Fatal("expected unknown-attribute error")
	}
	if !strings.Contains(err.Error(), "dsnn") {
		t.Errorf("error should name the typo: %v", err)
	}
}

// TestValidateConfigsRequiredMissing: a required attribute must be
// present, and the error must name the missing one so authors don't
// have to dig through the provider source to learn what's expected.
func TestValidateConfigsRequiredMissing(t *testing.T) {
	r := NewRegistry()
	p := &stubProvider{
		name:         "db",
		configSchema: map[string]protocol.AttrSchema{"dsn": {Type: "string", Required: true}},
	}
	_ = r.Register("db", p)

	err := r.ValidateConfigs(map[string]map[string]cty.Value{"db": {}})
	if err == nil {
		t.Fatal("expected missing-required error")
	}
	if !strings.Contains(err.Error(), "dsn") {
		t.Errorf("error should name the missing attr: %v", err)
	}
}

// TestValidateConfigsCoerces: string-typed attrs that arrive as other
// cty types get coerced by the registry before reaching the provider.
// Matches the action-attr behavior from evalAttrs.
func TestValidateConfigsCoerces(t *testing.T) {
	r := NewRegistry()
	p := &stubProvider{
		name:         "svc",
		configSchema: map[string]protocol.AttrSchema{"port": {Type: "number"}},
	}
	_ = r.Register("svc", p)

	if err := r.Configure(context.Background(), map[string]map[string]cty.Value{
		"svc": {"port": cty.StringVal("8080")},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if p.configCalls != 1 {
		t.Fatalf("Configure never reached the provider")
	}
}

// TestValidateConfigsNoSchemaRejectsInput: a provider with no config
// schema accepts no config. Tolerating extras here would let a typo
// like `provider "http" { baseurl = ... }` (real attr: base_url)
// silently do nothing.
func TestValidateConfigsNoSchemaRejectsInput(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("x", &stubProvider{name: "x"})

	err := r.ValidateConfigs(map[string]map[string]cty.Value{
		"x": {"anything": cty.StringVal("v")},
	})
	if err == nil {
		t.Fatal("expected rejection")
	}
}

// TestValidateConfigsAppliesDefaults: optional attrs with declared
// defaults reach the provider with the default value when the
// scenario omits them.
func TestValidateConfigsAppliesDefaults(t *testing.T) {
	r := NewRegistry()
	p := &stubProvider{
		name: "svc",
		configSchema: map[string]protocol.AttrSchema{
			"region": {Type: "string", Default: []byte(`"us-east-1"`)},
		},
	}
	_ = r.Register("svc", p)

	if err := r.Configure(context.Background(), nil); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if len(p.sawConfigKeys) != 1 || p.sawConfigKeys[0] != "region" {
		t.Errorf("expected default region to reach provider, got %v", p.sawConfigKeys)
	}
}

// TestConfigureResolvesPathAttributes: relative values of `path`
// config attributes are joined onto the base directory (the scenario
// file's), while absolute paths, ':' names, nulls and non-path
// attributes pass through.
func TestConfigureResolvesPathAttributes(t *testing.T) {
	base := filepath.Join(t.TempDir(), "scenarios")
	abs := filepath.Join(t.TempDir(), "elsewhere.db")
	schema := map[string]protocol.AttrSchema{
		"file": {Type: "string", Path: true},
		"name": {Type: "string"},
	}
	cases := []struct {
		name    string
		baseDir string
		in      cty.Value
		want    cty.Value
	}{
		{"relative", base, cty.StringVal("./data/dev.db"), cty.StringVal(filepath.Join(base, "data", "dev.db"))},
		{"parent", base, cty.StringVal("../dev.db"), cty.StringVal(filepath.Join(filepath.Dir(base), "dev.db"))},
		{"absolute", base, cty.StringVal(abs), cty.StringVal(abs)},
		{"special name", base, cty.StringVal(":memory:"), cty.StringVal(":memory:")},
		{"null", base, cty.NullVal(cty.String), cty.NullVal(cty.String)},
		{"no base dir", "", cty.StringVal("dev.db"), cty.StringVal("dev.db")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRegistry()
			r.SetBaseDir(c.baseDir)
			p := &stubProvider{name: "db", configSchema: schema}
			if err := r.Register("db", p); err != nil {
				t.Fatal(err)
			}
			if err := r.Configure(context.Background(), map[string]map[string]cty.Value{
				"db": {"file": c.in, "name": cty.StringVal("dev.db")},
			}); err != nil {
				t.Fatalf("Configure: %v", err)
			}
			if got := p.sawConfig["file"]; !got.RawEquals(c.want) {
				t.Fatalf("file: got %#v want %#v", got, c.want)
			}
			if got := p.sawConfig["name"]; !got.RawEquals(cty.StringVal("dev.db")) {
				t.Fatalf("non-path attribute changed: %#v", got)
			}
		})
	}
}

// Package provider defines the interface that both builtin and
// external providers implement.
//
// A provider is responsible for executing one or more action types
// against its target system (a database, an HTTP API, a command
// shell). The engine evaluates HCL action bodies into plain cty
// values and hands providers a flat, fully-resolved attribute map,
// so providers never see hcl.Body or hcl.EvalContext directly.
//
// The same interface is satisfied by builtins compiled into the
// orchard binary and by external subprocess providers (the stdio
// adapter lives in pkg/provider/external). That symmetry is
// intentional — it means the engine has one code path, and the same
// schema-driven validation covers every provider regardless of how
// it runs.
package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/pthm/orchard/pkg/protocol"
)

// Provider executes actions and advertises the schema that describes them.
//
// Lifecycle: after registration the engine calls Configure once with
// the evaluated `provider "name" {}` config from the scenario, then
// makes zero or more Execute calls, and finally calls Close during
// engine shutdown. Configure may be called with a nil/empty map when
// the provider declares no config attributes.
type Provider interface {
	// Schema returns the provider's self-description. It must be
	// stable across a single process lifetime — the engine caches it
	// after Configure and uses it to validate action bodies.
	Schema() protocol.ProviderSchema

	// Configure initializes the provider with evaluated config values.
	// It is called exactly once, before any Execute calls. Providers
	// that require no configuration may ignore the argument.
	Configure(ctx context.Context, config map[string]cty.Value) error

	// Execute performs the named action with the given attribute map
	// and returns the action's outputs. The engine has already
	// validated attrs against the schema, so providers can assume
	// required fields are present and types match.
	Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error)

	// Close releases any resources the provider holds (database
	// connections, subprocess handles, ...). It is called during
	// engine shutdown even if Configure failed, so implementations
	// must tolerate being called on a partially-initialized provider.
	Close() error
}

// Registry holds the set of providers available to an engine run,
// keyed by their scenario-local name (the alias chosen in
// `required_providers`). Two provider instances built from the
// same source can coexist under different names.
//
// A Registry goes through three phases: providers are added with
// Register, then Configure is called to initialize each one with the
// config block from the scenario, then Execute dispatches actions to
// the right provider. Close tears everything down in reverse order.
type Registry struct {
	mu         sync.Mutex
	providers  map[string]Provider
	order      []string // registration order, used for deterministic Close reverse-walk
	configured bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds p under the given local name. The name is the
// alias the scenario chose in its `required_providers` block —
// not the provider's schema name. A scenario that declares
//
//	required_providers {
//	  db = { source = "builtin/postgres" }
//	}
//
// registers the postgres provider under the name "db". This is
// what lets two instances of the same provider coexist, and what
// makes the `provider "db" {}` config block, the routing
// disambiguator (`provider = "db"` on an action), and the
// Configure lookup all agree.
//
// Returns an error on duplicate local names or after Configure
// has run — late registration would bypass the one-shot Configure
// contract.
func (r *Registry) Register(name string, p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.configured {
		return fmt.Errorf("provider registry: Register called after Configure")
	}
	if name == "" {
		return fmt.Errorf("provider registry: provider name is empty")
	}
	if p.Schema().Name == "" {
		return fmt.Errorf("provider registry: provider %q has empty schema name", name)
	}
	if _, dup := r.providers[name]; dup {
		return fmt.Errorf("provider registry: duplicate provider %q", name)
	}
	r.providers[name] = p
	r.order = append(r.order, name)
	return nil
}

// Get returns the registered provider with the given name.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return p, nil
}

// Names returns the registered provider names in registration order.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Configure initializes every registered provider with the config
// values supplied by the scenario. Providers with no entry in configs
// receive a nil map, which matches the "empty config" contract.
//
// Configure is one-shot: the first call flips the registry into the
// configured state and further Register calls are rejected. A single
// provider's Configure failure aborts the whole registry and causes
// subsequent calls to this method to return the same error without
// re-running Configure on any provider.
func (r *Registry) Configure(ctx context.Context, configs map[string]map[string]cty.Value) error {
	coerced, err := r.validateAndCoerceConfigs(configs)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.configured {
		r.mu.Unlock()
		return fmt.Errorf("provider registry: Configure already called")
	}
	r.configured = true
	order := append([]string{}, r.order...)
	r.mu.Unlock()

	for _, name := range order {
		p := r.providers[name]
		if err := p.Configure(ctx, coerced[name]); err != nil {
			return fmt.Errorf("configure provider %q: %w", name, err)
		}
	}
	return nil
}

// ValidateConfigs runs the schema-driven validation that Configure
// performs, without actually initializing any provider. `orchard
// plan` uses this to surface config typos at plan time — Configure
// itself is reserved for `orchard run`, since builtins like
// postgres open a live connection there.
//
// The validation is strict: unknown attributes are rejected,
// required-but-missing attributes are flagged, and every value is
// coerced to its declared cty type. A provider with no config
// schema accepts no attributes; passing one is an error.
func (r *Registry) ValidateConfigs(configs map[string]map[string]cty.Value) error {
	_, err := r.validateAndCoerceConfigs(configs)
	return err
}

// validateAndCoerceConfigs returns per-provider config maps that
// have been checked and coerced against each provider's schema.
// Configure relies on this so providers receive canonical values;
// ValidateConfigs re-uses the same pass to offer plan-time checks.
func (r *Registry) validateAndCoerceConfigs(configs map[string]map[string]cty.Value) (map[string]map[string]cty.Value, error) {
	r.mu.Lock()
	order := append([]string{}, r.order...)
	schemas := make(map[string]map[string]protocol.AttrSchema, len(r.providers))
	for name, p := range r.providers {
		schemas[name] = p.Schema().Config
	}
	r.mu.Unlock()

	out := make(map[string]map[string]cty.Value, len(order))
	for _, name := range order {
		coerced, err := validateProviderConfig(schemas[name], configs[name])
		if err != nil {
			return nil, fmt.Errorf("provider %q config: %w", name, err)
		}
		out[name] = coerced
	}
	return out, nil
}

// validateProviderConfig enforces one provider's config schema.
// Rejects unknown attributes, errors on missing required ones,
// fills declared defaults when the attribute is omitted, and
// coerces each supplied value to its declared type.
//
// An empty schema means the provider accepts no config. Scenarios
// that still pass attributes trip the "unknown attribute" error
// here rather than seeing their typo silently discarded.
func validateProviderConfig(schema map[string]protocol.AttrSchema, raw map[string]cty.Value) (map[string]cty.Value, error) {
	if len(schema) == 0 {
		if len(raw) > 0 {
			return nil, fmt.Errorf("provider accepts no config attributes, got %s",
				sortedValueKeys(raw))
		}
		return map[string]cty.Value{}, nil
	}

	for name := range raw {
		if _, ok := schema[name]; !ok {
			return nil, fmt.Errorf("unknown attribute %q (declared: %s)",
				name, sortedSchemaKeys(schema))
		}
	}

	out := make(map[string]cty.Value, len(schema))
	for name, attr := range schema {
		declared, err := attr.CtyType()
		if err != nil {
			return nil, fmt.Errorf("schema type for %q: %w", name, err)
		}
		val, present := raw[name]
		if !present {
			if len(attr.Default) > 0 {
				def, err := protocol.UnmarshalValue(attr.Default, declared)
				if err != nil {
					return nil, fmt.Errorf("default for %q: %w", name, err)
				}
				out[name] = def
				continue
			}
			if attr.Required {
				return nil, fmt.Errorf("missing required attribute %q", name)
			}
			continue
		}
		if declared != cty.DynamicPseudoType {
			coerced, err := convert.Convert(val, declared)
			if err != nil {
				return nil, fmt.Errorf("attribute %q: %w", name, err)
			}
			val = coerced
		}
		out[name] = val
	}
	return out, nil
}

func sortedValueKeys(m map[string]cty.Value) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedSchemaKeys(m map[string]protocol.AttrSchema) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// Close calls Close on every provider in reverse registration order.
// Errors are collected into a single error value so that one
// provider's Close failure does not prevent cleanup of the others —
// mirroring how defer cleanups compose in normal Go code.
func (r *Registry) Close() error {
	r.mu.Lock()
	order := append([]string{}, r.order...)
	providers := r.providers
	r.mu.Unlock()

	var errs []error
	for i := len(order) - 1; i >= 0; i-- {
		if err := providers[order[i]].Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %q: %w", order[i], err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	// Compose a single error without pulling in errors.Join's stack —
	// the registry is a boundary type and a flat message is fine.
	msg := "provider registry: close errors:"
	for _, e := range errs {
		msg += "\n  " + e.Error()
	}
	return fmt.Errorf("%s", msg)
}

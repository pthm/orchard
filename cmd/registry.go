package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/provider/external"
)

// providerMode selects the strategy registerProviders uses for
// external (subprocess) providers. Builtins are unaffected — they
// run in-process and have no side effects from instantiation.
type providerMode int

const (
	// providerModeRun spawns external providers as long-lived
	// subprocesses. Used by `orchard run`, where the provider needs
	// to accept Configure and Execute calls.
	providerModeRun providerMode = iota

	// providerModePlan queries external providers for their schema
	// via the `<binary> schema` subcommand and returns a
	// schema-only SchemaProvider. Used by `orchard plan`, which is
	// contractually side-effect-free: starting a long-lived
	// subprocess (and leaving it alive until Close) isn't
	// acceptable for a pure type-check.
	providerModePlan
)

// registerProviders instantiates every required provider declared
// by the scenario and registers it with a fresh registry. The
// caller chooses the instantiation mode: providerModeRun for
// live, configurable providers; providerModePlan for schema-only
// providers that refuse Configure and Execute.
//
// Providers are NOT configured here — callers that need live
// connections (orchard run) must call Configure themselves;
// plan-mode callers can skip it entirely. Factored out of
// buildRegistry so plan-time validation does not open database
// connections or other side-effectful Configure work just to
// type-check action bodies.
func registerProviders(ps *parser.ParsedScenario, mode providerMode) (*provider.Registry, error) {
	// Iterate in sorted order so registration (and therefore Close
	// reverse-order and plan output) is deterministic across runs.
	// Go map iteration is deliberately randomised; the scenarios'
	// intent is declarative so alphabetical is the least surprising
	// default.
	names := make([]string, 0, len(ps.RequiredProviders))
	for name := range ps.RequiredProviders {
		names = append(names, name)
	}
	sort.Strings(names)

	reg := provider.NewRegistry()
	reg.SetBaseDir(ps.ScenarioDir)
	for _, name := range names {
		spec := ps.RequiredProviders[name]
		p, err := instantiateProvider(spec.Source, mode)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
		// Register under the scenario-local name, not p.Schema().Name —
		// this is what allows aliases (db = builtin/postgres) to flow
		// through to Configure and routing.
		if err := reg.Register(name, p); err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
	}
	return reg, nil
}

// instantiateProvider resolves a source string to a Provider. The
// source scheme selects the backend; the mode selects how
// subprocess providers are instantiated:
//
//   - "builtin/<name>"        → provider.NewBuiltin (compiled-in,
//     same behaviour in either mode).
//   - "exec:<path>" (run)     → external.Spawn. Long-lived
//     subprocess, full Provider surface.
//   - "exec:<path>" (plan)    → external.LoadSchema. Short-lived
//     probe that only returns the provider schema; Configure and
//     Execute error so plan can't accidentally call them.
//
// Spawn failures (binary missing, bad handshake, invalid schema)
// propagate without leaving zombie processes. LoadSchema is
// bounded by a short timeout so a broken binary doesn't wedge
// plan indefinitely.
func instantiateProvider(source string, mode providerMode) (provider.Provider, error) {
	switch {
	case strings.HasPrefix(source, "builtin/"):
		return provider.NewBuiltin(source)
	case strings.HasPrefix(source, "exec:"):
		path := strings.TrimPrefix(source, "exec:")
		if path == "" {
			return nil, fmt.Errorf("exec source requires a path (got %q)", source)
		}
		if mode == providerModePlan {
			return external.LoadSchema(path)
		}
		return external.Spawn(path)
	default:
		return nil, fmt.Errorf("unknown provider source %q (expected builtin/<name> or exec:<path>)", source)
	}
}

package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/provider/external"
)

// registerProviders instantiates every required provider declared by
// the scenario and registers it with a fresh registry. Providers are
// NOT configured — callers that need live connections (orchard run)
// must call Configure themselves; callers that only need schemas
// (orchard plan) can skip it entirely.
//
// Factored out of buildRegistry so plan-time validation does not
// open database connections or other side-effectful Configure work
// just to type-check action bodies.
func registerProviders(ps *parser.ParsedScenario) (*provider.Registry, error) {
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
	for _, name := range names {
		spec := ps.RequiredProviders[name]
		p, err := instantiateProvider(spec.Source)
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

// instantiateProvider resolves a source string to a live Provider.
// The source scheme determines the backend:
//
//   - "builtin/<name>"  → provider.NewBuiltin (compiled-in)
//   - "exec:<path>"     → external.Spawn (subprocess over stdio)
//
// Spawning an external provider blocks on the describe handshake,
// so this is the boundary where subprocesses go live. If Spawn
// fails (binary missing, crashed during describe, bad schema),
// the error propagates without leaving zombie processes.
func instantiateProvider(source string) (provider.Provider, error) {
	switch {
	case strings.HasPrefix(source, "builtin/"):
		return provider.NewBuiltin(source)
	case strings.HasPrefix(source, "exec:"):
		path := strings.TrimPrefix(source, "exec:")
		if path == "" {
			return nil, fmt.Errorf("exec source requires a path (got %q)", source)
		}
		return external.Spawn(path)
	default:
		return nil, fmt.Errorf("unknown provider source %q (expected builtin/<name> or exec:<path>)", source)
	}
}

// buildRegistry is intentionally not provided as a single-call
// helper anymore. Callers must register providers first (which for
// exec: sources spawns subprocesses that need cleanup even on
// failure), defer Close, and only then call Configure — otherwise
// a failed Configure leaves running subprocesses or open pools
// that nothing will clean up. See cmd/run.go and cmd/plan.go for
// the correct order.
//
// The split is deliberate despite the small amount of caller
// boilerplate — bundling register+configure+defer into one helper
// encouraged the leak pattern in the first place.

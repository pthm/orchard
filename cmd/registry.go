package cmd

import (
	"context"
	"fmt"
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
	reg := provider.NewRegistry()
	for name, spec := range ps.RequiredProviders {
		p, err := instantiateProvider(spec.Source)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
		if err := reg.Register(p); err != nil {
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

// buildRegistry registers every declared provider and runs Configure
// with the scenario's evaluated provider configs. Used by orchard
// run, which needs providers fully ready before executing actions.
func buildRegistry(ctx context.Context, ps *parser.ParsedScenario) (*provider.Registry, error) {
	reg, err := registerProviders(ps)
	if err != nil {
		return nil, err
	}
	if err := reg.Configure(ctx, ps.ProviderConfigs); err != nil {
		return nil, err
	}
	return reg, nil
}

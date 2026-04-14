package cmd

import (
	"context"
	"fmt"

	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
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
		p, err := provider.NewBuiltin(spec.Source)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
		if err := reg.Register(p); err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
	}
	return reg, nil
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

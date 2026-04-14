package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"

	// Side-effect imports register builtin provider sources. Adding
	// a new builtin provider is a one-line change here.
	_ "github.com/pthm/orchard/pkg/provider/http"
	_ "github.com/pthm/orchard/pkg/provider/postgres"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <scenario>",
		Short: "Execute a scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			ps, err := parser.LoadScenario(args[0], varFlags)
			if err != nil {
				return fmt.Errorf("loading scenario: %w", err)
			}

			fmt.Printf("Scenario: %s\n", ps.Scenario.Name)

			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			// Register first, defer Close, then Configure. This order
			// matters: Spawn on an exec: source launches a subprocess
			// during Register, and Configure on any provider can
			// fail. If we configured before deferring Close, a
			// Configure error would leak the already-started
			// providers.
			reg, err := registerProviders(ps)
			if err != nil {
				return fmt.Errorf("registering providers: %w", err)
			}
			defer func() {
				if closeErr := reg.Close(); closeErr != nil {
					fmt.Printf("warning: %v\n", closeErr)
				}
			}()

			if err := reg.Configure(ctx, ps.ProviderConfigs); err != nil {
				return fmt.Errorf("configuring providers: %w", err)
			}

			eng, err := engine.New(reg)
			if err != nil {
				return fmt.Errorf("building engine: %w", err)
			}

			fmt.Println("Executing...")
			result, err := eng.Execute(ctx, ps, g)
			if err != nil {
				return fmt.Errorf("execution failed: %w", err)
			}

			if len(result.Outputs) > 0 {
				fmt.Println("\nOutputs:")
				for name, val := range result.Outputs {
					fmt.Printf("  %s = %s\n", name, formatCtyValue(val))
				}
			}

			fmt.Println("\nDone.")
			return nil
		},
	}
}

func formatCtyValue(v cty.Value) string {
	if v.IsNull() {
		return "null"
	}
	switch v.Type() {
	case cty.String:
		return v.AsString()
	case cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return fmt.Sprintf("%d", i)
		}
		f, _ := bf.Float64()
		return fmt.Sprintf("%g", f)
	case cty.Bool:
		return fmt.Sprintf("%t", v.True())
	default:
		return v.GoString()
	}
}

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/config"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/provider/postgres"
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

			// Build the provider registry and configure each provider
			// before the engine starts executing. Configure establishes
			// connections and validates credentials; failing here is
			// cheaper than failing inside the first action.
			reg := provider.NewRegistry()
			if err := reg.Register(postgres.New()); err != nil {
				return fmt.Errorf("registering postgres provider: %w", err)
			}

			dsn := config.ResolveDSN(dsnFlag)
			configs := map[string]map[string]cty.Value{
				"postgres": {"dsn": cty.StringVal(dsn)},
			}
			if err := reg.Configure(ctx, configs); err != nil {
				return fmt.Errorf("configuring providers: %w", err)
			}
			defer func() {
				if closeErr := reg.Close(); closeErr != nil {
					fmt.Printf("warning: %v\n", closeErr)
				}
			}()

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

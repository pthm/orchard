package cmd

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/config"
	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/internal/provider"
	sqlprovider "github.com/pthm/orchard/internal/provider/sql"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <scenario>",
		Short: "Execute a scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Parse scenario.
			ps, err := parser.LoadScenario(args[0], varFlags)
			if err != nil {
				return fmt.Errorf("loading scenario: %w", err)
			}

			fmt.Printf("Scenario: %s\n", ps.Scenario.Name)

			// Build dependency graph.
			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			// Connect to database.
			dsn := config.ResolveDSN(dsnFlag)
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer pool.Close()

			// Register providers.
			reg := provider.NewRegistry()
			reg.Register("sql", sqlprovider.NewProvider(pool))

			// Execute.
			fmt.Println("Executing...")
			eng := engine.New(reg)
			result, err := eng.Execute(ctx, ps, g)
			if err != nil {
				return fmt.Errorf("execution failed: %w", err)
			}

			// Print outputs.
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

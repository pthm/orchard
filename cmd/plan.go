package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/engine"
	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
)

func newPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <scenario>",
		Short: "Validate a scenario and show its execution plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := parser.LoadScenario(args[0], varFlags)
			if err != nil {
				return fmt.Errorf("loading scenario: %w", err)
			}

			fmt.Printf("Scenario: %s\n\n", ps.Scenario.Name)

			if len(ps.Variables) > 0 {
				names := make([]string, 0, len(ps.Variables))
				for name := range ps.Variables {
					names = append(names, name)
				}
				sort.Strings(names)
				fmt.Println("Variables:")
				for _, name := range names {
					fmt.Printf("  %s = %s\n", name, formatCtyValue(ps.Variables[name]))
				}
				fmt.Println()
			}

			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			// Register providers for schemas only; do not Configure.
			// Plan must not open database connections or make external
			// calls — it is a read-only type-checking pass. External
			// providers DO start subprocesses during Register (the
			// describe handshake reads the schema), so Close still
			// matters — defer it immediately.
			reg, err := registerProviders(ps)
			if err != nil {
				return fmt.Errorf("registering providers: %w", err)
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

			if err := eng.ValidateGraph(g); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}

			fmt.Println("Execution order:")
			for i, nodeID := range g.Order {
				node := g.Nodes[nodeID]
				deps := ""
				if len(node.Dependencies) > 0 {
					deps = fmt.Sprintf(" (depends on: %v)", node.Dependencies)
				}
				kind := "action"
				if node.Kind == graph.ComponentNode {
					kind = "component"
				}
				fmt.Printf("  %d. [%s] %s%s\n", i+1, kind, nodeID, deps)
			}

			if len(ps.Scenario.Outputs) > 0 {
				fmt.Println("\nOutputs:")
				for _, output := range ps.Scenario.Outputs {
					fmt.Printf("  %s\n", output.Name)
				}
			}

			fmt.Println("\nPlan is valid.")
			return nil
		},
	}
}

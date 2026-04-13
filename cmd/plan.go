package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
)

func newPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <scenario>",
		Short: "Show the execution plan for a scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ps, err := parser.LoadScenario(args[0], varFlags)
			if err != nil {
				return fmt.Errorf("loading scenario: %w", err)
			}

			fmt.Printf("Scenario: %s\n\n", ps.Scenario.Name)

			// Print variables.
			if len(ps.Variables) > 0 {
				fmt.Println("Variables:")
				for name, val := range ps.Variables {
					fmt.Printf("  %s = %s\n", name, formatCtyValue(val))
				}
				fmt.Println()
			}

			g, err := graph.Build(ps)
			if err != nil {
				return fmt.Errorf("building graph: %w", err)
			}

			// Print execution order.
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

			// Print outputs.
			if len(ps.Scenario.Outputs) > 0 {
				fmt.Println("\nOutputs:")
				for _, output := range ps.Scenario.Outputs {
					fmt.Printf("  %s\n", output.Name)
				}
			}

			return nil
		},
	}
}

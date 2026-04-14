package cmd

import (
	"github.com/spf13/cobra"
)

var (
	varFlags map[string]string
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "orchard",
		Short: "Scenario-based data orchestration tool",
		Long:  "Define and execute reproducible application state using HCL scenarios.",
	}

	root.PersistentFlags().StringToStringVar(&varFlags, "var", nil, "Variable overrides (key=value)")

	root.AddCommand(newRunCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newListCmd())

	return root
}

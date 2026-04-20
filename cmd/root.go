package cmd

import (
	"github.com/spf13/cobra"
)

var (
	varFlags    map[string]string
	noColorFlag bool
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "orchard",
		Short: "Scenario-based data orchestration tool",
		Long:  "Define and execute reproducible application state using HCL scenarios.",
		// Prevent cobra from printing usage on every RunE error.
		// Usage is helpful for wrong flags; it's noise after a
		// runtime error ("cannot connect to postgres") where the
		// user already knows how to invoke the command.
		SilenceUsage: true,
	}

	root.PersistentFlags().StringToStringVar(&varFlags, "var", nil, "Variable overrides (key=value)")
	root.PersistentFlags().BoolVar(&noColorFlag, "no-color", false, "Disable colored output (also honors NO_COLOR env)")

	root.AddCommand(newInitCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newDocsCmd())
	root.AddCommand(newTeardownCmd())
	root.AddCommand(newFunctionsCmd())

	return root
}

package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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
	root.AddCommand(newInspectCmd())
	root.AddCommand(newSchemaCmd())
	root.AddCommand(newFunctionsCmd())
	root.AddCommand(newVersionCmd())

	return root
}

// ExecuteWithSignals runs root with a context that Ctrl-C or SIGTERM
// cancels, so an interrupted run fails its current node through the
// normal failure path and still writes its record. Once the first
// signal lands the handler is removed, so a second Ctrl-C kills the
// process straight away.
//
// ponytail: exec: providers share our process group, so a terminal
// Ctrl-C reaches them too and they may exit before Orchard asks them
// to; give them their own process group if that ever matters.
func ExecuteWithSignals(root *cobra.Command) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	return root.ExecuteContext(ctx)
}

package cmd

import (
	"github.com/spf13/cobra"

	"github.com/pthm/orchard/internal/parser"
)

var seedFlag uint64

func addSeedFlag(cmd *cobra.Command) {
	cmd.Flags().Uint64Var(&seedFlag, "seed", 0,
		"Seed for random functions (overrides the scenario's seed; default random)")
}

// seedOption passes --seed to the parser when it was given.
func seedOption(cmd *cobra.Command) []parser.Option {
	if !cmd.Flags().Changed("seed") {
		return nil
	}
	return []parser.Option{parser.WithSeed(seedFlag)}
}

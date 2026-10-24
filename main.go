package main

import (
	"os"

	"github.com/pthm/orchard/cmd"
)

func main() {
	if err := cmd.ExecuteWithSignals(cmd.NewRootCmd()); err != nil {
		os.Exit(1)
	}
}

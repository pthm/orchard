package main

import (
	"os"

	"github.com/pthm/orchard/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

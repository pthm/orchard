package sqlite

import "github.com/pthm/orchard/pkg/provider"

// Registers sqlite as a builtin so scenarios can declare
//
//	required_providers { sqlite = { source = "builtin/sqlite" } }
//
// and have the runtime instantiate it. Packages that want the sqlite
// provider available as a builtin import this package blank-ly
// (`_ "github.com/pthm/orchard/pkg/provider/sqlite"`).
func init() {
	provider.RegisterBuiltin("builtin/sqlite", func() provider.Provider {
		return New()
	})
}

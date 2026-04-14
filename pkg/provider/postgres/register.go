package postgres

import "github.com/pthm/orchard/pkg/provider"

// Registers postgres as a builtin so scenarios can declare
//
//	required_providers { postgres = { source = "builtin/postgres" } }
//
// and have the runtime instantiate it. Packages that want the
// postgres provider available as a builtin import this package
// blank-ly (`_ "github.com/pthm/orchard/pkg/provider/postgres"`).
func init() {
	provider.RegisterBuiltin("builtin/postgres", func() provider.Provider {
		return New()
	})
}

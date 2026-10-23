package mysql

import "github.com/pthm/orchard/pkg/provider"

// Registers mysql as a builtin so scenarios can declare
//
//	required_providers { mysql = { source = "builtin/mysql" } }
//
// and have the runtime instantiate it. Packages that want the mysql
// provider available as a builtin import this package blank-ly
// (`_ "github.com/pthm/orchard/pkg/provider/mysql"`).
func init() {
	provider.RegisterBuiltin("builtin/mysql", func() provider.Provider {
		return New()
	})
}

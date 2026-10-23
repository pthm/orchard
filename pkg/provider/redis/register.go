package redis

import "github.com/pthm/orchard/pkg/provider"

// Registers redis as a builtin so scenarios can declare
//
//	required_providers { redis = { source = "builtin/redis" } }
//
// and have the runtime instantiate it. Packages that want the redis
// provider available as a builtin import this package blank-ly
// (`_ "github.com/pthm/orchard/pkg/provider/redis"`).
func init() {
	provider.RegisterBuiltin("builtin/redis", func() provider.Provider {
		return New()
	})
}

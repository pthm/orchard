package s3

import "github.com/pthm/orchard/pkg/provider"

// Registers s3 as a builtin so scenarios can declare
//
//	required_providers { s3 = { source = "builtin/s3" } }
//
// and have the runtime instantiate it. Packages that want the s3
// provider available as a builtin import this package blank-ly
// (`_ "github.com/pthm/orchard/pkg/provider/s3"`).
func init() {
	provider.RegisterBuiltin("builtin/s3", func() provider.Provider {
		return New()
	})
}

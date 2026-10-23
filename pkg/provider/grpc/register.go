package grpc

import "github.com/pthm/orchard/pkg/provider"

// Registers grpc as a builtin so scenarios can declare
//
//	required_providers { grpc = { source = "builtin/grpc" } }
//
// and have the runtime instantiate it. Packages that want the grpc
// provider available as a builtin import this package blank-ly
// (`_ "github.com/pthm/orchard/pkg/provider/grpc"`).
func init() {
	provider.RegisterBuiltin("builtin/grpc", func() provider.Provider {
		return New()
	})
}

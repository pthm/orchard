package http

import "github.com/pthm/orchard/pkg/provider"

// Registers http as a builtin so scenarios can declare
//
//	required_providers { http = { source = "builtin/http" } }
func init() {
	provider.RegisterBuiltin("builtin/http", func() provider.Provider {
		return New()
	})
}

package exec

import "github.com/pthm/orchard/pkg/provider"

// Registers the exec provider as a builtin so scenarios can declare
//
//	required_providers { exec = { source = "builtin/exec" } }
func init() {
	provider.RegisterBuiltin("builtin/exec", func() provider.Provider {
		return New()
	})
}

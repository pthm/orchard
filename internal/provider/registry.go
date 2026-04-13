package provider

import "fmt"

// Registry maps action type names to providers.
type Registry struct {
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(actionType string, p Provider) {
	r.providers[actionType] = p
}

func (r *Registry) Get(actionType string) (Provider, error) {
	p, ok := r.providers[actionType]
	if !ok {
		return nil, fmt.Errorf("no provider registered for action type %q", actionType)
	}
	return p, nil
}

package provider

import (
	"fmt"
	"sort"
	"sync"
)

// BuiltinFactory constructs an unconfigured Provider. It is called
// each time a scenario declares the corresponding source — one
// instance per scenario — so factories should return fresh state
// rather than a shared singleton.
type BuiltinFactory func() Provider

var (
	builtinsMu sync.RWMutex
	builtins   = map[string]BuiltinFactory{}
)

// RegisterBuiltin registers a factory for a source string like
// "builtin/postgres". Provider packages call this from init to
// expose themselves to the runtime; cmd/run.go in turn imports each
// provider package for its side effects.
//
// Panics on duplicate registration — a clash between two builtins
// claiming the same source is a programming error that must be
// caught at binary startup, not silently papered over.
func RegisterBuiltin(source string, factory BuiltinFactory) {
	builtinsMu.Lock()
	defer builtinsMu.Unlock()
	if _, dup := builtins[source]; dup {
		panic(fmt.Sprintf("provider: duplicate builtin registration for %q", source))
	}
	builtins[source] = factory
}

// NewBuiltin instantiates the builtin provider registered under the
// given source string.
func NewBuiltin(source string) (Provider, error) {
	builtinsMu.RLock()
	defer builtinsMu.RUnlock()
	f, ok := builtins[source]
	if !ok {
		return nil, fmt.Errorf("unknown provider source %q", source)
	}
	return f(), nil
}

// BuiltinSources returns the registered source strings in sorted
// order. Intended for error messages ("known sources: X, Y, Z")
// and for future `orchard providers list` style commands.
func BuiltinSources() []string {
	builtinsMu.RLock()
	defer builtinsMu.RUnlock()
	out := make([]string, 0, len(builtins))
	for s := range builtins {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

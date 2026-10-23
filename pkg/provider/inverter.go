package provider

import "github.com/zclconf/go-cty/cty"

// Inverter is implemented by providers that know how to undo some of
// their actions, so `orchard teardown` can clean them up without an
// explicit `lifecycle { teardown {} }` block (for instance, a typed
// insert is undone by deleting the row by its primary key). It's
// optional.
//
// The engine calls Inverse after an action succeeds, with the action's
// attributes and outputs, and stores the returned invocation in the run
// record. Inverse returns nil when there's no automatic undo. It's
// called after the action's side effect has happened, so it can't fail
// the action; it should describe the undo from what it already knows,
// without I/O. (A provider that needs the state before the change, like
// a typed update's previous values, captures it while executing.)
type Inverter interface {
	Inverse(action string, attrs, outputs map[string]cty.Value, opts InverseOptions) *Invocation
}

// InverseOptions carries the scenario's say in automatic teardown.
type InverseOptions struct {
	// Requested is true when the action sets
	// `lifecycle { managed_teardown = true }`: record an undo even where
	// the provider wouldn't by default (a typed update of rows Orchard
	// didn't create).
	Requested bool
}

// Invocation is an action call to make later: the action type and its
// already-evaluated attributes.
type Invocation struct {
	Action string
	Attrs  map[string]cty.Value
}

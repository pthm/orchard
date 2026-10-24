package provider

import (
	"context"

	"github.com/zclconf/go-cty/cty"
)

// Undoer is implemented by providers that know how to undo some of
// their actions, so `orchard teardown` can clean them up without an
// explicit `lifecycle { teardown {} }` block (for instance, a typed
// insert is undone by deleting the row by its primary key). It's
// optional.
//
// The engine calls ExecuteUndo in place of Execute when the action has
// no explicit teardown block and hasn't opted out with
// `managed_teardown = false`. It runs the action as Execute does and
// also returns the invocation that undoes it, or nil when there's no
// automatic undo; the engine stores it in the run record once the
// outputs pass validation. Returning the undo from the same call keeps
// what a provider learns while executing (a typed update's previous
// values, whether a key already existed) local to that call, so
// concurrent actions don't share state.
type Undoer interface {
	ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, opts InverseOptions) (outputs map[string]cty.Value, undo *Invocation, err error)
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

package provider

import (
	"context"
	"errors"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/snapshot"
)

// ActionPlanner is implemented by providers whose actions can be
// checked against external state, such as a database's schema. It's
// optional; the engine calls it, when present, for each action before
// a run starts (and during `orchard plan`), so problems surface before
// any action has side effects.
//
// attrs are the action's evaluated attributes. Values that depend on
// other actions' outputs aren't known yet and are cty unknowns, of a
// known type where one can be inferred. PlanAction returns what it
// learned in a Plan; an error means the action is invalid.
//
// A provider with nothing to check an action against (no snapshot at
// plan time) returns an error wrapping ErrNoSchema; the engine treats
// that as "unchecked", not as a failure.
type ActionPlanner interface {
	PlanAction(ctx context.Context, action string, attrs map[string]cty.Value) (Plan, error)
}

// Plan is what an ActionPlanner learned about one action.
type Plan struct {
	// Outputs is the type of the action's outputs, or
	// cty.DynamicPseudoType (or the zero Type) when it can't say. A
	// concrete type lets references to the outputs be type-checked.
	Outputs cty.Type
	// Hints are non-fatal observations worth showing the author, such
	// as a foreign key set to a fixed value. `orchard plan` prints them.
	Hints []string
}

// ErrNoSchema is returned (wrapped) by PlanAction when a provider has
// no schema to check an action against.
var ErrNoSchema = errors.New("no schema to check against")

// PlanConfigurer is implemented by providers that check actions at
// plan time from a snapshot, since `orchard plan` doesn't call
// Configure. PlanConfigure receives the provider's evaluated config and
// its snapshot (nil when there isn't one). It must not open
// connections or have other side effects.
type PlanConfigurer interface {
	PlanConfigure(ctx context.Context, config map[string]cty.Value, snap *snapshot.Snapshot) error
}

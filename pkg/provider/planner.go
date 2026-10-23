package provider

import (
	"context"

	"github.com/zclconf/go-cty/cty"
)

// ActionPlanner is implemented by providers whose actions can be
// checked against external state, such as a database's schema. It's
// optional; the engine calls it, when present, for each action before
// a run starts, so problems surface before any action has side
// effects.
//
// attrs are the action's evaluated attributes. Values that depend on
// other actions' outputs aren't known yet and are cty unknowns, of a
// known type where one can be inferred. PlanAction returns the type of
// the action's outputs, or cty.DynamicPseudoType when it can't say, and
// must return (cty.DynamicPseudoType, nil) when it has nothing to
// check against (for instance, before Configure).
type ActionPlanner interface {
	PlanAction(ctx context.Context, action string, attrs map[string]cty.Value) (cty.Type, error)
}

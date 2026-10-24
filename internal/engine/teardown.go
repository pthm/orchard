package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/provider"
)

// Teardown undoes each action that succeeded in a prior `orchard run`,
// in the reverse of the order that run completed them. vars, compVars,
// recordedOutputs and undos are the state that run captured in its
// record: the scenario's resolved variables, each component's resolved
// inputs (keyed like ParsedScenario.Components), each succeeded node's
// outputs, and the undo invocations providers described, keyed by full
// node ID ("action.sql.create_merchant" etc.). A component missing from
// compVars (a record from before they were kept) has its inputs
// evaluated again from vars and recordedOutputs. Nodes in done were torn
// down by an earlier, partly failed teardown and are skipped. completed
// lists node IDs in the order the run completed them (the record's node
// order); nodes it doesn't name, or all nodes when it is nil, follow in
// reverse topological order.
//
// For each action, in order of precedence:
//  1. its `lifecycle { teardown { } }` body, evaluated and run against
//     its provider;
//  2. otherwise its recorded undo invocation (a typed insert's delete
//     by primary key), unless the scenario now says
//     `managed_teardown = false`;
//  3. otherwise nothing.
//
// Actions whose original run failed, actions that no longer route to a
// registered provider, and component nodes are skipped.
//
// Teardown is best-effort: failures on individual actions are
// logged via the reporter's NodeFailed hook but don't abort the
// walk. Teardown returns the nodes whose teardown succeeded, so the
// caller can record them, and the joined set of per-action errors,
// or nil if every attempted teardown succeeded.
//
// Eval context per action:
//   - var.<name>   — scenario variables, or for an action inside a
//     component, the component's inputs
//   - self.<field> — this action's recorded outputs
//   - action.<type>.<name>.<field> — any other action's recorded
//     outputs; inside a component, only the component's own actions
//
// The teardown body is evaluated against the same provider action
// schema as the outer action, so `lifecycle { teardown { query = "..." } }`
// on a sql action type-checks against the sql action's `query`
// attribute declaration.
//
// Note: Teardown accepts pre-decoded cty state rather than the
// record.Record itself so the engine stays independent of the
// record package — record imports engine for its Reporter, and we
// avoid the cycle by making the caller do the decoding with
// record's own helpers.
func (e *Engine) Teardown(
	ctx context.Context,
	ps *parser.ParsedScenario,
	g *graph.Graph,
	vars map[string]cty.Value,
	compVars map[string]map[string]cty.Value,
	recordedOutputs map[string]map[string]cty.Value,
	undos map[string]provider.Invocation,
	done map[string]bool,
	completed []string,
) ([]string, error) {
	e.seed = ps.Seed
	e.reporter.RunStarted(ps.Scenario.Name)
	start := time.Now()

	var errs []error
	var torn []string
	succeeded := 0
	failed := 0

	// ponytail: teardown is sequential even after a parallel run;
	// walk reverse dependencies with Execute's ready set if large
	// teardowns get slow.
	//
	// Walk in reverse so resources created later are torn down
	// first. For the merchant→product FK case this removes the
	// product before the merchant, avoiding the constraint error
	// the opposite order would produce. Completion order, not
	// g.Order: a node completes only after its dependencies, and
	// after a parallel run it is the order independent actions
	// really changed shared data in, so each undo restores what the
	// one before it saw.
	walk := make([]graph.NodeID, 0, len(g.Order))
	seen := map[graph.NodeID]bool{}
	for i := len(completed) - 1; i >= 0; i-- {
		id := graph.NodeID(completed[i])
		if g.Nodes[id] != nil && !seen[id] {
			seen[id] = true
			walk = append(walk, id)
		}
	}
	for i := len(g.Order) - 1; i >= 0; i-- {
		if !seen[g.Order[i]] {
			walk = append(walk, g.Order[i])
		}
	}
	for _, nodeID := range walk {
		node := g.Nodes[nodeID]

		if node.Kind != graph.ActionNode || done[string(nodeID)] {
			continue
		}
		var run func() error
		undo, hasUndo := undos[string(nodeID)]
		switch {
		case node.Teardown != nil:
			// Only teardown what actually ran successfully. Failed or
			// missing entries in the record imply nothing was created;
			// attempting teardown would at best be a no-op and at worst
			// fail with confusing errors.
			selfOutputs, ok := recordedOutputs[string(nodeID)]
			if !ok {
				continue
			}
			run = func() error {
				scopeVars, scopeOutputs := vars, recordedOutputs
				if key := node.ComponentKey; key != "" {
					scopeOutputs = scopedComponentResolved(key, recordedOutputs)
					var ok bool
					if scopeVars, ok = compVars[key]; !ok {
						var err error
						if scopeVars, err = e.resolveComponentInputs(ps.Components[key], vars, recordedOutputs); err != nil {
							return err
						}
					}
				}
				return e.teardownAction(ctx, node, scopeVars, selfOutputs, scopeOutputs)
			}
		case hasUndo && !managedTeardownOff(node):
			run = func() error { return e.runUndo(ctx, node, undo) }
		default:
			continue
		}

		e.reporter.NodeStarted(string(nodeID))
		nodeStart := time.Now()
		err := run()
		if err != nil {
			e.reporter.NodeFailed(string(nodeID), err)
			errs = append(errs, fmt.Errorf("%s%s: %w", nodeID, formatSourceLoc(node.SourceRange), err))
			failed++
			continue
		}
		// Teardowns usually have no outputs worth capturing; the
		// provider's Execute return is discarded. A future
		// enhancement could surface them via a teardown record.
		e.reporter.NodeCompleted(string(nodeID), nil, time.Since(nodeStart))
		torn = append(torn, string(nodeID))
		succeeded++
	}

	ok := len(errs) == 0
	var finalErr error
	if !ok {
		finalErr = errors.Join(errs...)
	}
	e.reporter.RunCompleted(RunSummary{
		Scenario:  ps.Scenario.Name,
		Duration:  time.Since(start),
		Succeeded: succeeded,
		Failed:    failed,
		OK:        ok,
		Err:       finalErr,
	})
	return torn, finalErr
}

// teardownAction evaluates one action's teardown body against its
// provider's action schema and dispatches the resulting attrs to
// the provider. The action's own outputs land in the eval context
// under `self.*` so teardown expressions like
// `DELETE FROM t WHERE id = ${self.id}` work.
func (e *Engine) teardownAction(
	ctx context.Context,
	node *graph.Node,
	vars map[string]cty.Value,
	selfOutputs map[string]cty.Value,
	allOutputs map[string]map[string]cty.Value,
) error {
	route, err := e.resolveRoute(node.ActionType, node.ProviderAlias)
	if err != nil {
		return err
	}
	p, err := e.registry.Get(route.providerAlias)
	if err != nil {
		return err
	}

	evalCtx := withInstance(buildTeardownEvalContext(vars, selfOutputs, allOutputs), node.Instance)

	attrs, err := evalAttrs(node.Teardown, route.schema, evalCtx, e.seed, string(node.ID)+".teardown")
	if err != nil {
		return fmt.Errorf("evaluating teardown: %w", err)
	}

	// Teardown respects action-level timeout if present. A separate
	// `lifecycle { teardown { timeout = ... } }` would be more
	// precise — deferred.
	execCtx := ctx
	if node.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, node.Timeout)
		defer cancel()
	}

	if _, err := p.Execute(execCtx, route.actionName, attrs); err != nil {
		return err
	}
	return nil
}

// runUndo runs a recorded undo invocation against the node's provider.
// The attributes were evaluated when the action ran, so there's nothing
// to evaluate now.
func (e *Engine) runUndo(ctx context.Context, node *graph.Node, undo provider.Invocation) error {
	route, err := e.resolveRoute(node.ActionType, node.ProviderAlias)
	if err != nil {
		return err
	}
	p, err := e.registry.Get(route.providerAlias)
	if err != nil {
		return err
	}
	execCtx := ctx
	if node.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, node.Timeout)
		defer cancel()
	}
	_, err = p.Execute(execCtx, undo.Action, undo.Attrs)
	return err
}

// buildTeardownEvalContext composes the reference namespaces
// available inside a teardown body. var.* and action.* mirror the
// runtime eval context; self.* is teardown-specific — it points at
// the outputs of the action whose teardown is being evaluated so
// authors can write `self.id` without restating the node's name.
func buildTeardownEvalContext(
	vars map[string]cty.Value,
	selfOutputs map[string]cty.Value,
	allOutputs map[string]map[string]cty.Value,
) *hcl.EvalContext {
	variables := map[string]cty.Value{}
	if len(vars) > 0 {
		variables["var"] = cty.ObjectVal(vars)
	}
	if len(selfOutputs) > 0 {
		variables["self"] = cty.ObjectVal(selfOutputs)
	}

	actionsByType := make(map[string]map[string]cty.Value)
	for id, outputs := range allOutputs {
		// Only bare action IDs make it into `action.<type>.<name>`.
		// Component-scoped action IDs (component.X.action.Y.Z) are
		// addressed through their component, not as top-level
		// actions.
		parts := strings.SplitN(id, ".", 3)
		if len(parts) < 3 || parts[0] != "action" {
			continue
		}
		typ, name := parts[1], parts[2]
		if actionsByType[typ] == nil {
			actionsByType[typ] = make(map[string]cty.Value)
		}
		actionsByType[typ][name] = cty.ObjectVal(outputs)
	}
	if v, ok := wrapGrouped(actionsByType); ok {
		variables["action"] = v
	}

	return &hcl.EvalContext{
		Variables: variables,
		Functions: parser.Functions(),
	}
}

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
)

// Teardown walks the scenario graph in reverse topological order and
// runs each action's `lifecycle { teardown { } }` body against its
// provider. vars + recordedOutputs are the state a prior
// `orchard run` captured in its record — the scenario's resolved
// variables, and each succeeded node's outputs keyed by full node
// ID ("action.sql.create_merchant" etc.).
//
// Only nodes that (a) have a teardown body, (b) have a matching
// entry in recordedOutputs, and (c) still route to a registered
// provider are processed. Actions without teardown bodies, actions
// whose original run failed, and component nodes are skipped.
//
// Teardown is best-effort: failures on individual actions are
// logged via the reporter's NodeFailed hook but don't abort the
// walk. The returned error is the joined set of per-action errors,
// or nil if every attempted teardown succeeded.
//
// Eval context per action:
//   - var.<name>   — scenario variables
//   - self.<field> — this action's recorded outputs
//   - action.<type>.<name>.<field> — any other action's recorded outputs
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
	recordedOutputs map[string]map[string]cty.Value,
) error {
	e.reporter.RunStarted(ps.Scenario.Name)
	start := time.Now()

	var errs []error
	succeeded := 0
	failed := 0

	// Walk in reverse so resources created later are torn down
	// first. For the merchant→product FK case this removes the
	// product before the merchant, avoiding the constraint error
	// the opposite order would produce.
	for i := len(g.Order) - 1; i >= 0; i-- {
		nodeID := g.Order[i]
		node := g.Nodes[nodeID]

		if node.Kind != graph.ActionNode {
			continue
		}
		if node.Teardown == nil {
			continue
		}
		// Only teardown what actually ran successfully. Failed or
		// missing entries in the record imply nothing was created;
		// attempting teardown would at best be a no-op and at worst
		// fail with confusing errors.
		selfOutputs, ok := recordedOutputs[string(nodeID)]
		if !ok {
			continue
		}

		e.reporter.NodeStarted(string(nodeID))
		nodeStart := time.Now()
		err := e.teardownAction(ctx, node, vars, selfOutputs, recordedOutputs)
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
	return finalErr
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

	evalCtx := buildTeardownEvalContext(vars, selfOutputs, allOutputs)

	attrs, err := evalAttrs(node.Teardown, route.schema, evalCtx)
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
	if len(actionsByType) > 0 {
		objs := make(map[string]cty.Value, len(actionsByType))
		for t, byName := range actionsByType {
			objs[t] = cty.ObjectVal(byName)
		}
		variables["action"] = cty.ObjectVal(objs)
	}

	return &hcl.EvalContext{
		Variables: variables,
		Functions: parser.StdFunctions(),
	}
}


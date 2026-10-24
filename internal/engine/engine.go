package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/internal/language"
	"github.com/pthm/orchard/internal/parser"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// scopedComponentResolved rewrites resolved node outputs for a
// component-scoped eval context: internal action node IDs
// ("component.<key>.action.<type>.<name>") are re-keyed to their
// bare "action.<type>.<name>" form so expressions inside the
// component see only their own siblings.
func scopedComponentResolved(componentKey string, resolved map[string]map[string]cty.Value) map[string]map[string]cty.Value {
	prefix := "component." + componentKey + ".action."
	out := make(map[string]map[string]cty.Value)
	for nodeID, outputs := range resolved {
		if bare, ok := strings.CutPrefix(nodeID, prefix); ok {
			out["action."+bare] = outputs
		}
	}
	return out
}

// Engine executes a scenario's dependency graph.
type Engine struct {
	registry *provider.Registry
	// routes maps a scenario-level action type (the first label in
	// `action "TYPE" "name" {}`) to the set of registered providers
	// that offer it. More than one entry is allowed — scenarios
	// disambiguate at the action level with `provider = "<alias>"`.
	// Built once at construction time from registered providers'
	// schemas.
	routes   map[string][]actionRoute
	reporter Reporter

	// scenarioDir is the absolute directory of the scenario file.
	// Set at the start of Execute/Teardown from ParsedScenario.
	// Used to resolve `file` attributes on actions to absolute
	// paths before dispatching to providers.
	scenarioDir string
	// seed is the run's random seed (ParsedScenario.Seed).
	seed uint64

	// planTypes holds output types that ActionPlanners reported during
	// ValidateReferences, by node; they take precedence over the static
	// schema's declared type. unchecked records providers whose planner
	// had no schema to check against.
	planTypes map[graph.NodeID]cty.Type
	unchecked map[string]bool
	hints     []string

	// planned holds, when SetKeepPlannedAttrs is on, every action's
	// attributes as ValidateReferences evaluated them, by node.
	keepPlanned bool
	planned     map[graph.NodeID]map[string]cty.Value

	// parallelism caps how many actions Execute runs at once; below 1
	// means 1.
	parallelism int
}

// UndoReporter is implemented by reporters that keep run records.
// After NodeCompleted, the engine passes it the invocation that undoes
// the action, when the action's provider described one and the action
// has no explicit teardown block and hasn't opted out with
// `managed_teardown = false`.
type UndoReporter interface {
	NodeUndo(nodeID string, undo provider.Invocation)
}

// ComponentVarsReporter is implemented by reporters that keep run
// records. The engine passes it each component's resolved inputs (its
// var.* namespace, keyed like ParsedScenario.Components) before the
// component's first action runs, so teardown can evaluate the
// component's teardown bodies against the same values.
type ComponentVarsReporter interface {
	ComponentVars(componentKey string, vars map[string]cty.Value)
}

// Reporter receives observability events from the engine's
// execution loop. The engine has no opinion on how events are
// surfaced — CLI, structured logs, test-silence, or a future TUI
// all implement this interface. Scenarios can take minutes to
// run; making this hook pluggable keeps the engine independent
// of presentation.
//
// All methods are best-effort. The engine never checks return
// values; Reporter implementations must not block or fail the
// run. The engine makes every call from one goroutine, even when
// actions run in parallel, so implementations need no locking.
type Reporter interface {
	// RunStarted fires once, before any node executes. scenario is
	// the name from the top-level scenario block.
	RunStarted(scenario string)

	// NodeStarted fires just before a node's action runs. Component
	// nodes also emit this so reporters can show component-level
	// progress alongside action progress. The id is the fully-
	// qualified graph node id (e.g. "action.sql.create" or
	// "component.merchant.base").
	NodeStarted(nodeID string)

	// NodeCompleted fires after a node finishes successfully.
	// duration measures wall-clock time spent in the node's own
	// work (action execute, or component output resolution) —
	// upstream wait time is excluded.
	//
	// outputs carries the node's resolved outputs: an action's
	// Execute return map for action nodes, the resolved component
	// outputs for component nodes. Presentation reporters typically
	// ignore it; the recording reporter uses it to persist each
	// node's contribution to a run record.
	NodeCompleted(nodeID string, outputs map[string]cty.Value, duration time.Duration)

	// NodeFailed fires when a node returns an error. The engine
	// returns that error to the caller too; this gives the
	// reporter a chance to present it with context (the node id
	// that failed) before the caller unwinds.
	NodeFailed(nodeID string, err error)

	// RunCompleted fires once, after the last node is done and
	// scenario outputs have been evaluated. ok is false when the
	// run aborted part-way.
	RunCompleted(summary RunSummary)
}

// RunSummary is the end-of-run event payload.
type RunSummary struct {
	Scenario  string
	Outputs   map[string]cty.Value
	Duration  time.Duration
	Succeeded int
	Failed    int
	OK        bool
	Err       error // non-nil when the run aborted
}

// NopReporter discards all events. Engine uses it by default so
// callers that do not care about progress pay zero cost.
type NopReporter struct{}

// RunStarted implements Reporter.
func (NopReporter) RunStarted(string) {}

// NodeStarted implements Reporter.
func (NopReporter) NodeStarted(string) {}

// NodeCompleted implements Reporter.
func (NopReporter) NodeCompleted(string, map[string]cty.Value, time.Duration) {}

// NodeFailed implements Reporter.
func (NopReporter) NodeFailed(string, error) {}

// RunCompleted implements Reporter.
func (NopReporter) RunCompleted(RunSummary) {}

// actionRoute records one (provider, action) candidate for a
// given scenario action type. When multiple candidates exist for
// an action type, the scenario must pick one explicitly via
// `provider = "<alias>"` on the action body.
type actionRoute struct {
	providerAlias string                // local name in the registry
	actionName    string                // schema's action name (usually == action type)
	schema        protocol.ActionSchema // used by evalAttrs and validate
}

// Result holds the outputs from executing a scenario.
type Result struct {
	Outputs map[string]cty.Value
}

// resolveFileAttrs rewrites any attribute named "file" from a
// relative path to an absolute path anchored at scenarioDir. This
// is the generic mechanism that lets providers declare a `file`
// attribute and receive an absolute path regardless of the user's
// cwd — the same convention applied to component source paths and
// exec: provider sources, extended to action bodies.
//
// Attributes named anything other than "file", or whose value is
// already absolute, are untouched. A zero scenarioDir (empty string)
// disables resolution — this happens in test scenarios where no
// real file was loaded.
func resolveFileAttrs(attrs map[string]cty.Value, scenarioDir string) {
	if scenarioDir == "" {
		return
	}
	v, ok := attrs["file"]
	if !ok || v.IsNull() || v.Type() != cty.String {
		return
	}
	p := v.AsString()
	if p == "" || filepath.IsAbs(p) {
		return
	}
	attrs["file"] = cty.StringVal(filepath.Join(scenarioDir, p))
}

// formatSourceLoc returns a " (filename:line)" suffix suitable for
// decorating a runtime error, or an empty string when the source
// range is zero-valued (in-memory scenarios, merged bodies without
// position info). Kept as a single formatter so every error-path
// call site uses identical formatting.
func formatSourceLoc(r hcl.Range) string {
	if r.Filename == "" {
		return ""
	}
	return fmt.Sprintf(" (%s:%d)", r.Filename, r.Start.Line)
}

// New constructs an Engine over the given provider registry. It also
// builds the action-type routing table, failing if two providers
// declare the same action name — that ambiguity must be resolved by
// the caller (via an explicit provider choice in the scenario,
// coming in a later milestone) before execution can proceed.
//
// The engine installs a NopReporter by default. Call SetReporter to
// receive progress events.
func New(registry *provider.Registry) (*Engine, error) {
	routes, err := buildRoutes(registry)
	if err != nil {
		return nil, err
	}
	return &Engine{registry: registry, routes: routes, reporter: NopReporter{}}, nil
}

// SetReporter installs a Reporter for execution events. Call before
// Execute; there is no synchronization around the reporter field.
func (e *Engine) SetReporter(r Reporter) {
	if r == nil {
		r = NopReporter{}
	}
	e.reporter = r
}

// SetParallelism sets how many independent actions Execute runs at
// once. The default, 1, runs them one at a time in g.Order.
func (e *Engine) SetParallelism(n int) { e.parallelism = n }

// SetKeepPlannedAttrs makes ValidateReferences evaluate every
// action's attributes, not only those a provider plans, and keep them
// for PlannedAttrs. `orchard plan --show-values` turns it on.
func (e *Engine) SetKeepPlannedAttrs(on bool) { e.keepPlanned = on }

// PlannedAttrs returns an action's attributes as the last
// ValidateReferences evaluated them: values that depend on other
// actions are unknown. Nil unless SetKeepPlannedAttrs is on.
func (e *Engine) PlannedAttrs(id graph.NodeID) map[string]cty.Value { return e.planned[id] }

// ResolveProvider exposes the engine's action-to-provider routing
// for tooling (the plan renderer, future "orchard describe"
// commands). It returns the local alias of the provider that
// will handle (actionType, providerAlias) or an error when the
// pair is ambiguous / unknown. providerAlias may be empty when
// the scenario did not explicitly disambiguate; a unique candidate
// is returned in that case.
func (e *Engine) ResolveProvider(actionType, providerAlias string) (string, error) {
	route, err := e.resolveRoute(actionType, providerAlias)
	if err != nil {
		return "", err
	}
	return route.providerAlias, nil
}

func buildRoutes(registry *provider.Registry) (map[string][]actionRoute, error) {
	routes := make(map[string][]actionRoute)
	for _, name := range registry.Names() {
		p, err := registry.Get(name)
		if err != nil {
			return nil, err
		}
		schema := p.Schema()
		for actionName, actionSchema := range schema.Actions {
			routes[actionName] = append(routes[actionName], actionRoute{
				providerAlias: name,
				actionName:    actionName,
				schema:        actionSchema,
			})
		}
	}
	return routes, nil
}

// resolveRoute picks the candidate for the given scenario action
// type. When the scenario supplied a `provider = "<alias>"`
// attribute, that alias must match exactly one candidate. When no
// alias is supplied, there must be exactly one candidate; any
// other situation is an authoring error surfaced here.
func (e *Engine) resolveRoute(actionType, providerAlias string) (actionRoute, error) {
	candidates := e.routes[actionType]
	if len(candidates) == 0 {
		return actionRoute{}, fmt.Errorf("no provider registered for action type %q", actionType)
	}
	aliasesOf := func() string {
		aliases := make([]string, len(candidates))
		for i, c := range candidates {
			aliases[i] = c.providerAlias
		}
		sort.Strings(aliases)
		return strings.Join(aliases, ", ")
	}
	if providerAlias != "" {
		for _, c := range candidates {
			if c.providerAlias == providerAlias {
				return c, nil
			}
		}
		return actionRoute{}, fmt.Errorf(
			"provider %q does not expose action %q (providers offering it: %s)",
			providerAlias, actionType, aliasesOf(),
		)
	}
	if len(candidates) > 1 {
		return actionRoute{}, fmt.Errorf(
			"action type %q is offered by multiple providers (%s); "+
				"add `provider = \"<alias>\"` to the action body to disambiguate",
			actionType, aliasesOf(),
		)
	}
	return candidates[0], nil
}

// Execute runs every node once its dependencies have completed, up to
// SetParallelism actions at a time, and returns scenario outputs.
//
// The calling goroutine is the coordinator: it owns resolved outputs,
// component inputs and counters, builds each action's eval context
// before handing it to a goroutine, and makes every reporter call, so
// reporters need no locking. Ready nodes are dispatched in g.Order
// order, so parallelism 1 runs exactly g.Order. After the first
// failure nothing new starts, but ctx isn't cancelled: actions in
// flight finish, so their side effects and undos reach the record.
func (e *Engine) Execute(ctx context.Context, ps *parser.ParsedScenario, g *graph.Graph) (*Result, error) {
	e.scenarioDir = ps.ScenarioDir
	e.seed = ps.Seed
	resolved := make(map[string]map[string]cty.Value)
	// compVars holds each component's resolved inputs, indexed by
	// component key. Populated lazily on first access so components
	// with input expressions that reference upstream action outputs
	// still resolve against the latest `resolved` map — the graph
	// already ensures those upstreams have run by the time we need
	// the inputs.
	compVars := make(map[string]map[string]cty.Value)

	runStart := time.Now()
	e.reporter.RunStarted(ps.Scenario.Name)
	succeeded := 0
	var errs []error

	fail := func(nodeID string, err error) {
		e.reporter.NodeFailed(nodeID, err)
		errs = append(errs, err)
	}

	// waiting counts each node's incomplete dependencies; ready holds
	// the g.Order indexes of nodes with none left, smallest first.
	index := make(map[graph.NodeID]int, len(g.Order))
	waiting := make(map[graph.NodeID]int, len(g.Order))
	dependents := make(map[graph.NodeID][]graph.NodeID)
	var ready []int
	for i, id := range g.Order {
		index[id] = i
		deps := g.Nodes[id].Dependencies
		waiting[id] = len(deps)
		for _, dep := range deps {
			dependents[dep] = append(dependents[dep], id)
		}
		if len(deps) == 0 {
			ready = append(ready, i)
		}
	}
	complete := func(nodeID graph.NodeID, outputs map[string]cty.Value, d time.Duration) {
		resolved[string(nodeID)] = outputs
		e.reporter.NodeCompleted(string(nodeID), outputs, d)
		succeeded++
		for _, dep := range dependents[nodeID] {
			waiting[dep]--
			if waiting[dep] == 0 {
				ready = append(ready, index[dep])
			}
		}
		sort.Ints(ready)
	}

	type result struct {
		node     *graph.Node
		outputs  map[string]cty.Value
		undo     *provider.Invocation
		err      error
		duration time.Duration
	}
	results := make(chan result)
	inFlight := 0
	parallelism := max(e.parallelism, 1)

	for {
		for len(errs) == 0 && len(ready) > 0 && inFlight < parallelism {
			nodeID := g.Order[ready[0]]
			ready = ready[1:]
			node := g.Nodes[nodeID]
			// An interrupted run stops before the next node rather than
			// relying on every provider to notice the cancelled ctx.
			if err := ctx.Err(); err != nil {
				fail(string(nodeID), err)
				break
			}

			if node.Kind == graph.ComponentNode {
				// Resolving outputs is pure evaluation: do it here.
				e.reporter.NodeStarted(string(nodeID))
				start := time.Now()
				outputs, err := e.resolveComponentOutputs(node, ps, compVars, resolved)
				if err != nil {
					fail(string(nodeID), fmt.Errorf("resolving outputs for %s%s: %w", nodeID, formatSourceLoc(node.SourceRange), err))
					break
				}
				complete(nodeID, outputs, time.Since(start))
				continue
			}

			if node.ComponentKey != "" {
				if _, ok := compVars[node.ComponentKey]; !ok {
					vars, err := e.resolveComponentInputs(ps.Components[node.ComponentKey], ps.Variables, resolved)
					if err != nil {
						fail(string(nodeID), fmt.Errorf("resolving inputs for component %q%s: %w", node.ComponentKey, formatSourceLoc(node.SourceRange), err))
						break
					}
					compVars[node.ComponentKey] = vars
					if cr, ok := e.reporter.(ComponentVarsReporter); ok {
						cr.ComponentVars(node.ComponentKey, vars)
					}
				}
			}
			evalCtx := e.buildNodeEvalContext(node, ps.Variables, compVars, resolved)
			e.reporter.NodeStarted(string(nodeID))
			inFlight++
			go func() {
				start := time.Now()
				outputs, undo, err := e.executeAction(ctx, node, evalCtx)
				results <- result{node, outputs, undo, err, time.Since(start)}
			}()
		}
		if inFlight == 0 {
			break
		}

		r := <-results
		inFlight--
		if r.err != nil {
			fail(string(r.node.ID), fmt.Errorf("executing %s%s: %w", r.node.ID, formatSourceLoc(r.node.SourceRange), r.err))
			continue
		}
		complete(r.node.ID, r.outputs, r.duration)
		if r.undo != nil {
			if ur, ok := e.reporter.(UndoReporter); ok {
				ur.NodeUndo(string(r.node.ID), *r.undo)
			}
		}
	}

	scenarioOutputs := make(map[string]cty.Value)
	if len(errs) == 0 {
		evalCtx := buildEvalContext(ps.Variables, resolved)
		for _, output := range ps.Scenario.Outputs {
			val, diags := output.Value.Value(language.Scoped(evalCtx, e.seed, "output."+output.Name))
			if diags.HasErrors() {
				fail("output."+output.Name, fmt.Errorf("evaluating output %q: %s", output.Name, diags.Error()))
				break
			}
			scenarioOutputs[output.Name] = val
		}
	}

	if len(errs) > 0 {
		err := errors.Join(errs...)
		e.reporter.RunCompleted(RunSummary{
			Scenario:  ps.Scenario.Name,
			Duration:  time.Since(runStart),
			Succeeded: succeeded,
			Failed:    len(errs),
			OK:        false,
			Err:       err,
		})
		return nil, err
	}

	e.reporter.RunCompleted(RunSummary{
		Scenario:  ps.Scenario.Name,
		Outputs:   scenarioOutputs,
		Duration:  time.Since(runStart),
		Succeeded: succeeded,
		OK:        true,
	})

	return &Result{Outputs: scenarioOutputs}, nil
}

// executeAction runs an action against its provider with evalCtx,
// retrying as its retry block says. It runs on a worker goroutine, so
// it reads nothing Execute's coordinator writes.
func (e *Engine) executeAction(
	ctx context.Context,
	node *graph.Node,
	evalCtx *hcl.EvalContext,
) (map[string]cty.Value, *provider.Invocation, error) {
	if node.Retry == nil {
		return e.executeActionOnce(ctx, node, evalCtx)
	}

	// Retry loop. Each attempt runs with its own per-action
	// timeout (not a cumulative deadline across attempts) — the
	// common use case for retry is "each call should finish in
	// 30s, give me up to 3 tries", not "spread 30s across tries".
	var lastErr error
	for attempt := 1; attempt <= node.Retry.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		outputs, undo, err := e.executeActionOnce(ctx, node, evalCtx)
		if err == nil {
			return outputs, undo, nil
		}
		lastErr = err
		if attempt == node.Retry.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(computeRetryDelay(node.Retry, attempt)):
		}
	}
	return nil, nil, fmt.Errorf("action %s failed after %d attempts: %w",
		node.ID, node.Retry.MaxAttempts, lastErr)
}

// executeActionOnce runs exactly one attempt of a node against
// its provider. Factored out so the retry wrapper can invoke it
// in a loop without duplicating the route/eval/timeout plumbing. It
// returns the action's undo, when its provider describes one (see
// provider.Undoer).
func (e *Engine) executeActionOnce(
	ctx context.Context,
	node *graph.Node,
	evalCtx *hcl.EvalContext,
) (map[string]cty.Value, *provider.Invocation, error) {
	route, err := e.resolveRoute(node.ActionType, node.ProviderAlias)
	if err != nil {
		return nil, nil, err
	}
	p, err := e.registry.Get(route.providerAlias)
	if err != nil {
		return nil, nil, err
	}

	attrs, err := evalAttrs(node.Body, route.schema, evalCtx, e.seed, string(node.ID))
	if err != nil {
		return nil, nil, err
	}

	// Resolve `file` attributes to absolute paths against the
	// scenario directory, so providers that read files work
	// regardless of the caller's cwd. Convention: any action
	// attribute named "file" that evaluates to a relative path
	// gets resolved here, before the provider sees it.
	resolveFileAttrs(attrs, e.scenarioDir)

	// Apply the action-level timeout (if any) on top of the
	// caller's ctx. Providers that respect ctx will see the
	// deadline; ones that ignore it will run to completion and
	// leak an unavoidable grace-period goroutine — documented
	// behaviour.
	execCtx := ctx
	if node.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, node.Timeout)
		defer cancel()
	}

	// An explicit teardown block wins over the provider's own undo.
	var (
		outputs map[string]cty.Value
		undo    *provider.Invocation
	)
	if u, ok := p.(provider.Undoer); ok && node.Teardown == nil && !managedTeardownOff(node) {
		opts := provider.InverseOptions{Requested: node.ManagedTeardown != nil && *node.ManagedTeardown}
		outputs, undo, err = u.ExecuteUndo(execCtx, route.actionName, attrs, opts)
	} else {
		outputs, err = p.Execute(execCtx, route.actionName, attrs)
	}
	if err != nil {
		// Rewrite deadline-exceeded to name the action and the
		// timeout that fired — generic "context deadline exceeded"
		// buries the signal scenario authors need.
		if errors.Is(err, context.DeadlineExceeded) && node.Timeout > 0 {
			return nil, nil, fmt.Errorf("action %s exceeded timeout %s: %w",
				node.ID, node.Timeout, err)
		}
		return nil, nil, err
	}
	validated, err := validateOutputs(route.schema, outputs)
	if err != nil {
		// Don't prefix with node.ID here — the caller in Execute
		// already wraps with "executing <nodeID> (file:line)".
		return nil, nil, fmt.Errorf("output validation: %w", err)
	}
	return validated, undo, nil
}

// managedTeardownOff reports `managed_teardown = false`.
func managedTeardownOff(node *graph.Node) bool {
	return node.ManagedTeardown != nil && !*node.ManagedTeardown
}

// validateOutputs enforces an action's declared output contract.
// Scenarios type-check references against the schema at plan time;
// without a runtime check a misbehaving provider could hand back
// values of the wrong shape and the mismatch would only surface in
// whatever downstream expression tried to use them — far from the
// real source. Instead, coerce here: reject unknown attributes and
// convert known ones to their declared types, so the error names
// the offending provider+action the moment the contract is broken.
//
// Actions whose schema declares `outputs { type = any }` or has no
// output block are opaque: plan-time type-checking already can't
// narrow them, so there's nothing to coerce — accept the map as-is.
func validateOutputs(schema protocol.ActionSchema, outputs map[string]cty.Value) (map[string]cty.Value, error) {
	if schema.Outputs == nil {
		return outputs, nil
	}
	declared, err := schema.Outputs.CtyType()
	if err != nil {
		// Schema itself is malformed; plan-time validation already
		// surfaced it. Don't double-report at execute time.
		return outputs, nil
	}
	if declared == cty.DynamicPseudoType {
		return outputs, nil
	}
	if !declared.IsObjectType() {
		return nil, fmt.Errorf("output schema must be an object type, got %s", declared.FriendlyName())
	}
	declaredAttrs := declared.AttributeTypes()
	for name := range outputs {
		if _, ok := declaredAttrs[name]; !ok {
			return nil, fmt.Errorf("unknown output attribute %q (declared: %s)",
				name, sortedAttrNames(declaredAttrs))
		}
	}
	result := make(map[string]cty.Value, len(declaredAttrs))
	for name, t := range declaredAttrs {
		val, present := outputs[name]
		if !present {
			// Plan-time typing assumes every declared attribute is
			// reachable; materialise a typed null so downstream
			// expressions see a consistently-shaped value rather
			// than a missing-key runtime surprise.
			result[name] = cty.NullVal(t)
			continue
		}
		coerced, err := convert.Convert(val, t)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", name, err)
		}
		result[name] = coerced
	}
	return result, nil
}

func sortedAttrNames(attrs map[string]cty.Type) string {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// computeRetryDelay returns the wait between attempt N and
// attempt N+1, given a retry configuration. Attempt numbers are
// 1-indexed — the first attempt runs immediately, the delay
// applies between attempt and the next one.
func computeRetryDelay(cfg *graph.RetryConfig, attempt int) time.Duration {
	var d time.Duration
	switch cfg.Backoff {
	case graph.BackoffConstant:
		d = cfg.Delay
	case graph.BackoffLinear:
		d = cfg.Delay * time.Duration(attempt)
	case graph.BackoffExponential:
		// delay * 2^(attempt-1). Guards against negative shift when
		// attempt is spuriously zero (should never happen).
		shift := attempt - 1
		if shift < 0 {
			shift = 0
		}
		d = cfg.Delay << shift
	default:
		d = cfg.Delay
	}
	if cfg.MaxDelay > 0 && d > cfg.MaxDelay {
		d = cfg.MaxDelay
	}
	return d
}

// buildNodeEvalContext creates an EvalContext appropriate for the node.
// Component-scoped actions see `var.*` as the component's resolved
// inputs (with defaults applied from the component's own variable
// declarations), and `action.*` references are rewritten to expose
// only the internal actions belonging to the same component.
func (e *Engine) buildNodeEvalContext(
	node *graph.Node,
	vars map[string]cty.Value,
	compVars map[string]map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) *hcl.EvalContext {
	if node.ComponentKey == "" {
		return withInstance(buildEvalContext(vars, resolved), node.Instance)
	}

	scopedVars := compVars[node.ComponentKey]
	if scopedVars == nil {
		// Component with no inputs block and no declared variables.
		scopedVars = map[string]cty.Value{}
	}
	return buildEvalContext(scopedVars, scopedComponentResolved(node.ComponentKey, resolved))
}

// resolveComponentOutputs evaluates a component's output blocks using
// its internal action results and the component-scoped var namespace.
//
// compVars is expected to already contain the component's resolved
// inputs — populated by Execute before this node runs, so that both
// internal actions and output blocks see the same var context.
func (e *Engine) resolveComponentOutputs(
	node *graph.Node,
	ps *parser.ParsedScenario,
	compVars map[string]map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	comp, ok := ps.Components[node.ComponentKey]
	if !ok {
		return nil, fmt.Errorf("component %q not found", node.ComponentKey)
	}

	scopedResolved := scopedComponentResolved(node.ComponentKey, resolved)

	vars, ok := compVars[node.ComponentKey]
	if !ok {
		// Component with neither inputs nor internal actions: nothing
		// to resolve, but we still need an empty var namespace so
		// output expressions referencing declared component defaults
		// evaluate cleanly.
		v, err := e.resolveComponentInputs(comp, ps.Variables, resolved)
		if err != nil {
			return nil, err
		}
		vars = v
	}

	evalCtx := buildEvalContext(vars, scopedResolved)

	outputs := make(map[string]cty.Value)
	for _, output := range comp.Def.Outputs {
		val, diags := output.Value.Value(language.Scoped(evalCtx, e.seed, node.ComponentKey+".output."+output.Name))
		if diags.HasErrors() {
			return nil, fmt.Errorf("evaluating component output %q: %s", output.Name, diags.Error())
		}
		outputs[output.Name] = val
	}

	return outputs, nil
}

// resolveComponentInputs evaluates the inputs block of a component reference
// in the scenario's scope, returning values to use as the component's var.* namespace.
//
// Validates three contract properties of the component:
//   - every input key matches a declared variable (no stray inputs);
//   - every required (no-default) declared variable has an input or default;
//   - every input value coerces to the declared variable's type.
//
// These checks turn silent "the input was ignored" / "the type drifted"
// bugs into authoring errors raised before the component's actions run.
func (e *Engine) resolveComponentInputs(
	comp *parser.ParsedComponent,
	scenarioVars map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) (map[string]cty.Value, error) {
	declsByName := make(map[string]*parser.VariableBlock, len(comp.Def.Variables))
	for _, v := range comp.Def.Variables {
		declsByName[v.Name] = v
	}
	compKey := comp.Ref.Type + "." + comp.Ref.Name

	result := make(map[string]cty.Value)

	// Apply coerced defaults first so inputs can override.
	for _, v := range comp.Def.Variables {
		declaredType, err := parser.ResolveVariableType(v)
		if err != nil {
			return nil, fmt.Errorf("component %q variable %q: %w", compKey, v.Name, err)
		}
		defaultExpr, err := parser.VariableDefault(v)
		if err != nil {
			return nil, fmt.Errorf("component %q variable %q: %w", compKey, v.Name, err)
		}
		if defaultExpr == nil {
			continue
		}
		val, diags := defaultExpr.Value(language.Scoped(nil, e.seed, compKey+".var."+v.Name))
		if diags.HasErrors() {
			return nil, fmt.Errorf("component %q variable %q default: %s",
				compKey, v.Name, diags.Error())
		}
		coerced, err := coerceVar(val, declaredType)
		if err != nil {
			return nil, fmt.Errorf("component %q variable %q default: %w", compKey, v.Name, err)
		}
		result[v.Name] = coerced
	}

	if comp.Ref.Inputs != nil {
		type inputsWrapper struct {
			Inputs map[string]cty.Value `hcl:"inputs,optional"`
		}
		scenarioCtx := withInstance(buildEvalContext(scenarioVars, resolved), comp.Instance)
		var wrapper inputsWrapper
		diags := gohcl.DecodeBody(comp.Ref.Inputs, language.Scoped(scenarioCtx, e.seed, compKey+".inputs"), &wrapper)
		if diags.HasErrors() {
			return nil, fmt.Errorf("component %q inputs: %s", compKey, diags.Error())
		}
		for name, val := range wrapper.Inputs {
			decl, ok := declsByName[name]
			if !ok {
				return nil, fmt.Errorf(
					"component %q: input %q is not a declared variable on component %q",
					compKey, name, comp.Def.Name,
				)
			}
			declaredType, err := parser.ResolveVariableType(decl)
			if err != nil {
				return nil, fmt.Errorf("component %q variable %q: %w", compKey, name, err)
			}
			coerced, err := coerceVar(val, declaredType)
			if err != nil {
				return nil, fmt.Errorf("component %q input %q: %w", compKey, name, err)
			}
			result[name] = coerced
		}
	}

	// Every declared variable must have either a default or an input.
	for name, decl := range declsByName {
		if _, present := result[name]; present {
			continue
		}
		defaultExpr, err := parser.VariableDefault(decl)
		if err != nil {
			return nil, err
		}
		if defaultExpr == nil {
			return nil, fmt.Errorf(
				"component %q: required variable %q has no default and no input",
				compKey, name,
			)
		}
	}

	return result, nil
}

// coerceVar is the engine-side twin of the parser's coerceToType.
// Kept local rather than exporting from parser so the engine has
// a single concept ("coerce against declared type, dynamic = accept
// as-is") without worrying about cyclic imports.
func coerceVar(val cty.Value, declared cty.Type) (cty.Value, error) {
	if declared == cty.DynamicPseudoType {
		return val, nil
	}
	coerced, err := convert.Convert(val, declared)
	if err != nil {
		return cty.NilVal, fmt.Errorf("type mismatch: %w", err)
	}
	return coerced, nil
}

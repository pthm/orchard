package engine

import (
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/parser"
)

// buildEvalContext constructs an HCL EvalContext from resolved variables
// and previously executed node outputs.
//
// The resulting context supports references like:
//
//	var.<name>
//	action.<type>.<name>.<output>
//	component.<type>.<name>.<output>
func buildEvalContext(
	vars map[string]cty.Value,
	resolved map[string]map[string]cty.Value,
) *hcl.EvalContext {
	variables := make(map[string]cty.Value)

	// var.* namespace
	if len(vars) > 0 {
		variables["var"] = cty.ObjectVal(vars)
	}

	// Group resolved outputs by namespace (action/component) and type.
	// NodeID format: "action.sql.create_user" or "component.merchant.base"
	actionsByType := make(map[string]map[string]cty.Value)
	componentsByType := make(map[string]map[string]cty.Value)

	for nodeID, outputs := range resolved {
		parts := strings.SplitN(nodeID, ".", 3)
		if len(parts) < 3 {
			continue
		}
		namespace, typ, name := parts[0], parts[1], parts[2]

		outputObj := cty.ObjectVal(outputs)

		switch namespace {
		case "action":
			if actionsByType[typ] == nil {
				actionsByType[typ] = make(map[string]cty.Value)
			}
			actionsByType[typ][name] = outputObj
		case "component":
			if componentsByType[typ] == nil {
				componentsByType[typ] = make(map[string]cty.Value)
			}
			componentsByType[typ][name] = outputObj
		}
	}

	if v, ok := wrapGrouped(actionsByType); ok {
		variables["action"] = v
	}
	if v, ok := wrapGrouped(componentsByType); ok {
		variables["component"] = v
	}

	return &hcl.EvalContext{
		Variables: variables,
		Functions: parser.Functions(),
	}
}

// wrapGrouped folds a two-level type→name→value map into a single
// cty object value. Returns false when the input is empty, so the
// caller can skip adding the namespace entirely.
func wrapGrouped(byType map[string]map[string]cty.Value) (cty.Value, bool) {
	if len(byType) == 0 {
		return cty.NilVal, false
	}
	typeObjs := make(map[string]cty.Value, len(byType))
	for typ, names := range byType {
		typeObjs[typ] = cty.ObjectVal(foldInstances(names))
	}
	return cty.ObjectVal(typeObjs), true
}

// foldInstances groups count / for_each instances under their base
// name: "payment[0]", "payment[1]" become a tuple at "payment", and
// `site["eu"]` entries become an object at "site". So scenarios
// reference action.sql.payment[0].id or action.sql.payment[*].id.
func foldInstances(byName map[string]cty.Value) map[string]cty.Value {
	out := make(map[string]cty.Value, len(byName))
	indexed := map[string]map[int]cty.Value{}
	keyed := map[string]map[string]cty.Value{}
	for name, v := range byName {
		base, suffix, ok := strings.Cut(name, "[")
		if !ok {
			out[name] = v
			continue
		}
		suffix = strings.TrimSuffix(suffix, "]")
		if k, err := strconv.Unquote(suffix); err == nil {
			if keyed[base] == nil {
				keyed[base] = map[string]cty.Value{}
			}
			keyed[base][k] = v
		} else if i, err := strconv.Atoi(suffix); err == nil {
			if indexed[base] == nil {
				indexed[base] = map[int]cty.Value{}
			}
			indexed[base][i] = v
		} else {
			out[name] = v
		}
	}
	for base, m := range keyed {
		out[base] = cty.ObjectVal(m)
	}
	for base, m := range indexed {
		n := 0
		for i := range m {
			n = max(n, i+1)
		}
		elems := make([]cty.Value, n)
		for i := range elems {
			if v, ok := m[i]; ok {
				elems[i] = v
			} else {
				// Gap from a failed instance in a partial run record.
				elems[i] = cty.NullVal(cty.DynamicPseudoType)
			}
		}
		out[base] = cty.TupleVal(elems)
	}
	return out
}

// withInstance layers an instance's count.* / each.* namespace over
// ctx. nil inst returns ctx unchanged.
func withInstance(ctx *hcl.EvalContext, inst *parser.Instance) *hcl.EvalContext {
	if inst == nil {
		return ctx
	}
	child := ctx.NewChild()
	child.Variables = inst.Vars()
	return child
}

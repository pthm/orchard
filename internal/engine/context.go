package engine

import (
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
		typeObjs[typ] = cty.ObjectVal(names)
	}
	return cty.ObjectVal(typeObjs), true
}

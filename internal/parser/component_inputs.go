package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ValidateComponentInputs runs plan-time checks on a component ref
// against its declared variables. Two invariants:
//
//   - Every input key must correspond to a declared variable on the
//     component. A typo like `inputs = { naem = var.x }` is caught
//     here, not at execution time.
//   - Every required variable (no declared default) must be present
//     as an input key.
//
// The value expressions themselves are NOT evaluated — they may
// reference action outputs that only exist at runtime. Type
// coercion of input values stays with the engine's runtime pass.
//
// When the inputs expression is not a literal object (e.g. a
// function call producing a map), key-set inspection is skipped
// gracefully and the engine's runtime pass covers it. Required-
// variable checks still run if the inputs body has no `inputs`
// attribute at all.
func ValidateComponentInputs(comp *ParsedComponent) error {
	compKey := comp.Ref.Type + "." + comp.Ref.Name

	decls := make(map[string]*VariableBlock, len(comp.Def.Variables))
	for _, v := range comp.Def.Variables {
		decls[v.Name] = v
	}

	keys, staticallyKnown, err := componentInputKeys(comp.Ref.Inputs)
	if err != nil {
		return fmt.Errorf("component %q: %w", compKey, err)
	}

	providedKeys := make(map[string]bool)
	if staticallyKnown {
		for _, k := range keys {
			if _, declared := decls[k]; !declared {
				return fmt.Errorf(
					"component %q: input %q is not a declared variable on component %q",
					compKey, k, comp.Def.Name)
			}
			providedKeys[k] = true
		}
	}

	// Required-variable check: every declared variable with no
	// default must be provided. When the inputs expression is not a
	// literal object (staticallyKnown == false), this check is
	// skipped — we cannot tell what keys the dynamic expression will
	// produce, and the engine's runtime pass will catch any missing
	// required variable before the component's actions run.
	if !staticallyKnown {
		return nil
	}
	for name, decl := range decls {
		if providedKeys[name] {
			continue
		}
		defaultExpr, err := VariableDefault(decl)
		if err != nil {
			return fmt.Errorf("component %q variable %q: %w", compKey, name, err)
		}
		if defaultExpr == nil {
			return fmt.Errorf(
				"component %q: required variable %q has no default and no input",
				compKey, name)
		}
	}
	return nil
}

// componentInputKeys returns the attribute names inside a
// `inputs = { ... }` expression if the value side is an object
// literal. The second return value reports whether the extraction
// was possible — a false result means the expression is more
// complex than a literal (e.g. a merge() call) and plan-time key
// checks should be skipped.
func componentInputKeys(body hcl.Body) ([]string, bool, error) {
	if body == nil {
		return nil, true, nil
	}
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return nil, false, fmt.Errorf("%s", diags.Error())
	}
	inputsAttr, ok := attrs["inputs"]
	if !ok {
		return nil, true, nil
	}
	obj, ok := inputsAttr.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil, false, nil
	}
	keys := make([]string, 0, len(obj.Items))
	for _, item := range obj.Items {
		name := hcl.ExprAsKeyword(item.KeyExpr)
		if name == "" {
			// Non-identifier key (e.g. dynamic, computed, string literal
			// with spaces). Treat as unknown shape — runtime catches it.
			return nil, false, nil
		}
		keys = append(keys, name)
	}
	return keys, true, nil
}

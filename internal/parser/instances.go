package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// Instance identifies one copy of an action or component expanded
// by `count` or `for_each`. Key is a number index for count and a
// string key for for_each; Value is for_each's element (unset for
// count).
type Instance struct {
	Key   cty.Value
	Value cty.Value
}

// Suffix renders the instance address appended to node IDs and
// component keys: `[0]` for count, `["key"]` for for_each.
func (i *Instance) Suffix() string {
	if i.Key.Type() == cty.Number {
		n, _ := i.Key.AsBigFloat().Int64()
		return fmt.Sprintf("[%d]", n)
	}
	return fmt.Sprintf("[%q]", i.Key.AsString())
}

// Vars returns the eval-context namespace the instance exposes:
// `count.index` for count, `each.key` / `each.value` for for_each.
func (i *Instance) Vars() map[string]cty.Value {
	if i.Key.Type() == cty.Number {
		return map[string]cty.Value{"count": cty.ObjectVal(map[string]cty.Value{"index": i.Key})}
	}
	return map[string]cty.Value{"each": cty.ObjectVal(map[string]cty.Value{"key": i.Key, "value": i.Value})}
}

// RepetitionSchema names the attributes ExpandInstances consumes.
var RepetitionSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "count"}, {Name: "for_each"}},
}

// ExpandInstances pulls `count` / `for_each` off body, evaluates it
// against the scenario's variables, and returns one Instance per
// copy plus the body with those attributes hidden.
//
// A body with neither attribute returns []*Instance{nil}: a single,
// unexpanded block. count = 0 or an empty for_each returns an empty
// slice.
//
// Both attributes must be known before anything runs, so they may
// only reference var.* and functions — never action or component
// outputs.
func ExpandInstances(body hcl.Body, vars map[string]cty.Value) ([]*Instance, hcl.Body, error) {
	if body == nil {
		return []*Instance{nil}, nil, nil
	}
	content, remain, diags := body.PartialContent(RepetitionSchema)
	if diags.HasErrors() {
		return nil, nil, fmt.Errorf("%s", diags.Error())
	}
	countAttr, hasCount := content.Attributes["count"]
	eachAttr, hasEach := content.Attributes["for_each"]
	switch {
	case hasCount && hasEach:
		return nil, nil, fmt.Errorf("count and for_each are mutually exclusive")
	case hasCount:
		insts, err := expandCount(countAttr, vars)
		return insts, remain, err
	case hasEach:
		insts, err := expandForEach(eachAttr, vars)
		return insts, remain, err
	}
	return []*Instance{nil}, remain, nil
}

func evalRepetition(attr *hcl.Attribute, vars map[string]cty.Value) (cty.Value, error) {
	for _, trav := range attr.Expr.Variables() {
		if root := trav.RootName(); root != "var" {
			return cty.NilVal, fmt.Errorf("%s: cannot reference %s.*; it may only reference var.* and literals, because it must be known before anything runs",
				attr.Name, root)
		}
	}
	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{"var": cty.ObjectVal(vars)},
		Functions: Functions(),
	}
	val, diags := attr.Expr.Value(ctx)
	if diags.HasErrors() {
		return cty.NilVal, fmt.Errorf("%s: %s", attr.Name, diags.Error())
	}
	if val.IsNull() {
		return cty.NilVal, fmt.Errorf("%s must not be null", attr.Name)
	}
	return val, nil
}

func expandCount(attr *hcl.Attribute, vars map[string]cty.Value) ([]*Instance, error) {
	val, err := evalRepetition(attr, vars)
	if err != nil {
		return nil, err
	}
	num, err := convert.Convert(val, cty.Number)
	if err != nil {
		return nil, fmt.Errorf("count must be a number, got %s", val.Type().FriendlyName())
	}
	bf := num.AsBigFloat()
	n, acc := bf.Int64()
	if !bf.IsInt() || acc != 0 || n < 0 {
		return nil, fmt.Errorf("count must be a whole number >= 0, got %s", bf.String())
	}
	insts := make([]*Instance, n)
	for i := range insts {
		insts[i] = &Instance{Key: cty.NumberIntVal(int64(i))}
	}
	return insts, nil
}

func expandForEach(attr *hcl.Attribute, vars map[string]cty.Value) ([]*Instance, error) {
	val, err := evalRepetition(attr, vars)
	if err != nil {
		return nil, err
	}
	ty := val.Type()
	var insts []*Instance
	switch {
	case ty.IsMapType() || ty.IsObjectType():
		// ElementIterator yields keys in sorted order, which keeps
		// instance ordering deterministic.
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			insts = append(insts, &Instance{Key: k, Value: v})
		}
	case ty.IsListType() || ty.IsSetType() || ty.IsTupleType():
		strs, err := convert.Convert(val, cty.List(cty.String))
		if err != nil {
			return nil, fmt.Errorf("for_each list or set must contain strings: %w", err)
		}
		seen := map[string]bool{}
		for it := strs.ElementIterator(); it.Next(); {
			_, v := it.Element()
			if v.IsNull() {
				return nil, fmt.Errorf("for_each must not contain null")
			}
			if seen[v.AsString()] {
				return nil, fmt.Errorf("for_each contains duplicate key %q", v.AsString())
			}
			seen[v.AsString()] = true
			insts = append(insts, &Instance{Key: v, Value: v})
		}
	default:
		return nil, fmt.Errorf("for_each must be a map, object, or list/set of strings, got %s", ty.FriendlyName())
	}
	if insts == nil {
		insts = []*Instance{}
	}
	return insts, nil
}

package engine

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/pthm/orchard/pkg/protocol"
)

// evalAttrs evaluates an HCL action body against a provider's action
// schema, producing the fully-resolved attribute map that gets handed
// to Provider.Execute.
//
// Responsibilities:
//   - Enforce the schema (unknown attributes are rejected as errors
//     with source locations, required attributes are verified present).
//   - Evaluate each declared attribute expression against evalCtx.
//   - Coerce the result to the attribute's declared cty.Type so
//     providers receive values of the shape they asked for.
//   - Apply defaults from the schema when an optional attribute is
//     omitted from the body.
//
// This is the one place in the codebase where HCL semantics meet
// provider semantics; keeping the logic centralized is what lets
// providers stay free of HCL knowledge.
func evalAttrs(
	body hcl.Body,
	schema protocol.ActionSchema,
	evalCtx *hcl.EvalContext,
) (map[string]cty.Value, error) {
	// Actions with no attributes and no body (test fixtures, future
	// no-op actions) are legal. Early-return avoids a nil-body panic
	// in hcl.Body.Content.
	if body == nil {
		if schema.Attrs == nil || len(schema.Attrs) == 0 {
			return map[string]cty.Value{}, nil
		}
		for _, attr := range schema.Attrs {
			if attr.Required {
				return nil, fmt.Errorf("action body is empty but schema declares required attributes")
			}
		}
	}

	bodySchema := &hcl.BodySchema{
		Attributes: make([]hcl.AttributeSchema, 0, len(schema.Attrs)),
	}
	for name, attr := range schema.Attrs {
		bodySchema.Attributes = append(bodySchema.Attributes, hcl.AttributeSchema{
			Name:     name,
			Required: attr.Required,
		})
	}

	var content *hcl.BodyContent
	if body != nil {
		var diags hcl.Diagnostics
		content, diags = body.Content(bodySchema)
		if diags.HasErrors() {
			return nil, fmt.Errorf("action body: %s", diags.Error())
		}
	} else {
		content = &hcl.BodyContent{}
	}

	out := make(map[string]cty.Value, len(schema.Attrs))
	for name, attrSchema := range schema.Attrs {
		declaredType, err := attrSchema.CtyType()
		if err != nil {
			return nil, fmt.Errorf("schema type for %q: %w", name, err)
		}

		hclAttr, present := content.Attributes[name]
		if !present {
			// Required-ness is enforced by body.Content above; if we
			// reach here the attribute is optional and missing. Apply
			// a default if the schema provides one; otherwise omit.
			if len(attrSchema.Default) > 0 {
				val, err := protocol.UnmarshalValue(attrSchema.Default, declaredType)
				if err != nil {
					return nil, fmt.Errorf("default for %q: %w", name, err)
				}
				out[name] = val
			}
			continue
		}

		val, diags := hclAttr.Expr.Value(evalCtx)
		if diags.HasErrors() {
			return nil, fmt.Errorf("evaluating %q: %s", name, diags.Error())
		}

		// Skip coercion when the schema accepts anything — convert.Convert
		// rejects DynamicPseudoType targets outright.
		if declaredType != cty.DynamicPseudoType {
			converted, err := convert.Convert(val, declaredType)
			if err != nil {
				return nil, fmt.Errorf("attribute %q: %s", name, err)
			}
			val = converted
		}
		out[name] = val
	}
	return out, nil
}

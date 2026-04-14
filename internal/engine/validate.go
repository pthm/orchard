package engine

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/pthm/orchard/internal/graph"
	"github.com/pthm/orchard/pkg/protocol"
)

// ValidateGraph runs structural schema validation on every action
// node in the graph without executing anything. It catches the
// errors that are worth surfacing at plan time — unknown attribute
// names, missing required attributes, and action types that no
// registered provider handles — while deferring runtime-dependent
// checks (expression evaluation, type coercion against upstream
// outputs) to execution.
//
// Safe to call before Configure. The method reads only the routing
// table built from provider schemas, so providers do not need live
// connections to validate a plan.
//
// Returns the first error found. A multi-error aggregator would be
// a natural upgrade once callers start wanting "all problems at
// once" reporting, but for today the single-error form maps
// cleanly onto command-line tooling.
func (e *Engine) ValidateGraph(g *graph.Graph) error {
	for _, nodeID := range g.Order {
		node := g.Nodes[nodeID]
		if node.Kind != graph.ActionNode {
			continue
		}
		route, err := e.resolveRoute(node.ActionType, node.ProviderAlias)
		if err != nil {
			return fmt.Errorf("%s: %w", nodeID, err)
		}
		if err := validateActionBody(node.Body, route.schema); err != nil {
			return fmt.Errorf("%s: %w", nodeID, err)
		}
	}
	return nil
}

// validateActionBody performs schema-driven structural checks on an
// action body. It does not evaluate expressions — references like
// action.sql.create.id have not produced values yet at plan time —
// so type-coercion errors surface at execution time.
func validateActionBody(body hcl.Body, schema protocol.ActionSchema) error {
	if body == nil {
		for name, attr := range schema.Attrs {
			if attr.Required {
				return fmt.Errorf("missing required attribute %q", name)
			}
		}
		return nil
	}

	_, diags := body.Content(bodySchemaFromActionSchema(schema))
	if diags.HasErrors() {
		return fmt.Errorf("action body: %s", diags.Error())
	}
	return nil
}

// bodySchemaFromActionSchema builds the hcl.BodySchema used to parse
// an action body against the provider's declared attributes.
func bodySchemaFromActionSchema(schema protocol.ActionSchema) *hcl.BodySchema {
	bs := &hcl.BodySchema{
		Attributes: make([]hcl.AttributeSchema, 0, len(schema.Attrs)),
	}
	for name, attr := range schema.Attrs {
		bs.Attributes = append(bs.Attributes, hcl.AttributeSchema{
			Name:     name,
			Required: attr.Required,
		})
	}
	return bs
}

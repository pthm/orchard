package provider

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// Provider executes an action and returns its outputs.
type Provider interface {
	Execute(ctx context.Context, body hcl.Body, evalCtx *hcl.EvalContext) (map[string]cty.Value, error)
}

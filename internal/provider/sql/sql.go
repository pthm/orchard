package sql

import (
	"context"
	"fmt"
	"math/big"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zclconf/go-cty/cty"
)

type sqlAction struct {
	Query string `hcl:"query"`
}

// Provider executes SQL queries against PostgreSQL.
type Provider struct {
	pool *pgxpool.Pool
}

func NewProvider(pool *pgxpool.Pool) *Provider {
	return &Provider{pool: pool}
}

func (p *Provider) Execute(ctx context.Context, body hcl.Body, evalCtx *hcl.EvalContext) (map[string]cty.Value, error) {
	var action sqlAction
	diags := gohcl.DecodeBody(body, evalCtx, &action)
	if diags.HasErrors() {
		return nil, fmt.Errorf("decoding sql action: %s", diags.Error())
	}

	rows, err := p.pool.Query(ctx, action.Query)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}
	defer rows.Close()

	outputs := make(map[string]cty.Value)
	if rows.Next() {
		descs := rows.FieldDescriptions()
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("reading row values: %w", err)
		}
		for i, desc := range descs {
			outputs[string(desc.Name)] = toCtyValue(values[i])
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration: %w", err)
	}

	return outputs, nil
}

func toCtyValue(v interface{}) cty.Value {
	switch val := v.(type) {
	case string:
		return cty.StringVal(val)
	case int32:
		return cty.NumberIntVal(int64(val))
	case int64:
		return cty.NumberIntVal(val)
	case float32:
		return cty.NumberFloatVal(float64(val))
	case float64:
		return cty.NumberFloatVal(val)
	case bool:
		return cty.BoolVal(val)
	case nil:
		return cty.NullVal(cty.String)
	default:
		// For numeric types that pgx may return (e.g., pgtype.Numeric).
		if n, ok := v.(*big.Float); ok {
			f, _ := n.Float64()
			return cty.NumberFloatVal(f)
		}
		return cty.StringVal(fmt.Sprintf("%v", val))
	}
}

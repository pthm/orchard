package postgres

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"
)

// executeSQL runs a raw query and captures the first returned row as
// a flat map of column name → cty.Value. Queries that return zero
// rows produce an empty outputs map, which is the right signal for
// INSERTs without RETURNING and for DDL statements.
func (p *Provider) executeSQL(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	query, ok := attrs["query"]
	if !ok {
		return nil, fmt.Errorf("postgres.sql: query is required")
	}
	if query.Type() != cty.String {
		return nil, fmt.Errorf("postgres.sql: query must be a string, got %s", query.Type().FriendlyName())
	}

	rows, err := p.pool.Query(ctx, query.AsString())
	if err != nil {
		return nil, fmt.Errorf("postgres.sql: execute: %w", err)
	}
	defer rows.Close()

	outputs := make(map[string]cty.Value)
	if rows.Next() {
		descs := rows.FieldDescriptions()
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("postgres.sql: read row: %w", err)
		}
		for i, desc := range descs {
			outputs[string(desc.Name)] = toCtyValue(values[i])
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.sql: row iteration: %w", err)
	}
	return outputs, nil
}

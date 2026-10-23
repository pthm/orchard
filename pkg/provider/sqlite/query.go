package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/zclconf/go-cty/cty"
)

// executeQuery runs a statement and returns its outputs:
//
//   - If the statement returns columns (a SELECT, or a write with
//     RETURNING), the first row keyed by column name. No rows → empty.
//   - Otherwise (a plain INSERT, UPDATE, DELETE or DDL),
//     last_insert_id and rows_affected, read on the same connection
//     right after the statement.
func (p *Provider) executeQuery(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	query, ok := attrs["query"]
	if !ok || query.IsNull() {
		return nil, fmt.Errorf("sqlite_query: query is required")
	}
	if query.Type() != cty.String {
		return nil, fmt.Errorf("sqlite_query: query must be a string, got %s", query.Type().FriendlyName())
	}

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite_query: connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	rows, err := conn.QueryContext(ctx, query.AsString())
	if err != nil {
		return nil, fmt.Errorf("sqlite_query: execute: %w", err)
	}
	cols, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("sqlite_query: columns: %w", err)
	}

	outputs := make(map[string]cty.Value)
	if rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite_query: read row: %w", err)
		}
		for i, col := range cols {
			outputs[col] = toCtyValue(values[i])
		}
	}
	// SQLite applies every change, RETURNING included, on the first
	// step, so closing after one row doesn't lose writes.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite_query: close rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite_query: row iteration: %w", err)
	}

	if len(cols) == 0 {
		var id, affected int64
		err := conn.QueryRowContext(ctx, "SELECT last_insert_rowid(), changes()").Scan(&id, &affected)
		if err != nil {
			return nil, fmt.Errorf("sqlite_query: read insert id: %w", err)
		}
		outputs["last_insert_id"] = cty.NumberIntVal(id)
		outputs["rows_affected"] = cty.NumberIntVal(affected)
	}
	return outputs, nil
}

// toCtyValue converts a value from the driver into a cty.Value.
// modernc.org/sqlite returns SQLite's storage classes as int64,
// float64, string, []byte and nil, plus time.Time for columns
// declared DATE, DATETIME or TIMESTAMP. Anything else falls back to
// its string form, matching the postgres provider's permissive
// approach.
func toCtyValue(v any) cty.Value {
	switch val := v.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	case string:
		return cty.StringVal(val)
	case []byte:
		return cty.StringVal(string(val))
	case int64:
		return cty.NumberIntVal(val)
	case float64:
		return cty.NumberFloatVal(val)
	case bool:
		return cty.BoolVal(val)
	case time.Time:
		return cty.StringVal(val.Format(time.RFC3339Nano))
	default:
		return cty.StringVal(fmt.Sprintf("%v", val))
	}
}

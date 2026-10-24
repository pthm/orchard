package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/sqlrow"
)

// executeSQL runs a raw query and captures the first returned row as
// a flat map of column name → cty.Value. Queries that return zero
// rows produce an empty outputs map, which is the right signal for
// INSERTs without RETURNING and for DDL statements.
//
// The query goes over the simple protocol, so it may hold several
// statements; Postgres runs them as one implicit transaction unless
// they include their own BEGIN/COMMIT. Outputs come from the first
// statement's result.
func (p *Provider) executeSQL(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	query, ok := attrs["query"]
	if !ok {
		return nil, fmt.Errorf("postgres.sql: query is required")
	}
	if query.Type() != cty.String {
		return nil, fmt.Errorf("postgres.sql: query must be a string, got %s", query.Type().FriendlyName())
	}

	// One connection for the query and the transaction check after it.
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres.sql: acquire connection: %w", err)
	}
	defer conn.Release()

	// ponytail: simple protocol only reads the first statement's rows;
	// walk pgconn results if outputs from later statements are needed.
	rows, err := conn.Query(ctx, query.AsString(), pgx.QueryExecModeSimpleProtocol)
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
			outputs[desc.Name] = sqlrow.ToCty(values[i])
		}
	}
	// Close drains the remaining statements, so a later one failing
	// shows up in Err.
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.sql: row iteration: %w", err)
	}
	// A BEGIN without COMMIT would otherwise be rolled back silently
	// when the pool resets the connection, after reporting success.
	if conn.Conn().PgConn().TxStatus() != 'I' {
		if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
			return nil, fmt.Errorf("postgres.sql: query left a transaction open; rollback: %w", err)
		}
		return nil, fmt.Errorf("postgres.sql: query left a transaction open (missing COMMIT?); rolled back")
	}
	return outputs, nil
}

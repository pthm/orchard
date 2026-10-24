package sqlite

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/sqlrow"
)

// statusQuery reports the outputs of a statement that returned no
// rows. last_insert_rowid() and changes() are per-connection, which is
// why the provider runs everything on one.
const statusQuery = "SELECT last_insert_rowid(), changes()"

// executeQuery runs a statement and returns its first row, or
// last_insert_id and rows_affected if it returned no rows. SQLite
// applies every change, RETURNING included, on the first step, so
// reading one row doesn't lose writes.
func (p *Provider) executeQuery(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	query, ok := attrs["query"]
	if !ok || query.IsNull() {
		return nil, fmt.Errorf("sqlite_query: query is required")
	}
	if query.Type() != cty.String {
		return nil, fmt.Errorf("sqlite_query: query must be a string, got %s", query.Type().FriendlyName())
	}

	args, err := sqlrow.Params(attrs, sqlrow.Arg)
	if err != nil {
		return nil, fmt.Errorf("sqlite_query: %w", err)
	}

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite_query: connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	outputs, err := sqlrow.Query(ctx, conn, query.AsString(), statusQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite_query: %w", err)
	}
	return outputs, nil
}

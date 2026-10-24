package mysql

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/sqlrow"
)

// statusQuery reports the outputs of a statement that returned no
// rows. Both functions are per-connection, so it runs on the same
// connection as the statement.
const statusQuery = "SELECT LAST_INSERT_ID(), ROW_COUNT()"

// executeQuery runs a statement and returns its first row, or
// last_insert_id and rows_affected if it returned no rows.
func (p *Provider) executeQuery(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	query, ok := attrs["query"]
	if !ok || query.IsNull() {
		return nil, fmt.Errorf("mysql_query: query is required")
	}
	if query.Type() != cty.String {
		return nil, fmt.Errorf("mysql_query: query must be a string, got %s", query.Type().FriendlyName())
	}

	args, err := sqlrow.Params(attrs, sqlrow.Arg)
	if err != nil {
		return nil, fmt.Errorf("mysql_query: %w", err)
	}

	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("mysql_query: connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	outputs, err := sqlrow.Query(ctx, conn, query.AsString(), statusQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql_query: %w", err)
	}
	return outputs, nil
}

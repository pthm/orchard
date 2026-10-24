package postgres

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/sqlrow"
)

// executeSQL runs a raw query and captures the first returned row as
// a flat map of column name → cty.Value. Queries that return zero
// rows produce an empty outputs map, which is the right signal for
// INSERTs without RETURNING and for DDL statements.
//
// Without params the query goes over the simple protocol, so it may
// hold several statements; Postgres runs them as one implicit
// transaction unless they include their own BEGIN/COMMIT. Outputs
// come from the first statement's result.
//
// With params it is prepared and the values bound to $1, $2, ... on
// the server, which takes exactly one statement.
func (p *Provider) executeSQL(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	query, ok := attrs["query"]
	if !ok {
		return nil, fmt.Errorf("postgres.sql: query is required")
	}
	if query.Type() != cty.String {
		return nil, fmt.Errorf("postgres.sql: query must be a string, got %s", query.Type().FriendlyName())
	}

	args, err := sqlrow.Params(attrs, arg)
	if err != nil {
		return nil, fmt.Errorf("postgres.sql: %w", err)
	}
	queryArgs := []any{pgx.QueryExecModeSimpleProtocol}
	if args != nil {
		queryArgs = args
	}

	// One connection for the query and the transaction check after it.
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres.sql: acquire connection: %w", err)
	}
	defer conn.Release()

	// ponytail: simple protocol only reads the first statement's rows;
	// walk pgconn results if outputs from later statements are needed.
	rows, err := conn.Query(ctx, query.AsString(), queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("postgres.sql: execute: %w%s", err, multiStatementHint(err, args))
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
			outputs[desc.Name] = pgValue(values[i])
		}
	}
	// Close drains the remaining statements, so a later one failing
	// shows up in Err.
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.sql: row iteration: %w%s", err, multiStatementHint(err, args))
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

// arg converts a param like sqlrow.Arg, except that a list goes as a
// Postgres array, for `= ANY($1)` and array columns.
func arg(v cty.Value) (any, error) {
	if v.IsKnown() && !v.IsNull() && (v.Type().IsListType() || v.Type().IsTupleType() || v.Type().IsSetType()) {
		elems := v.AsValueSlice()
		out := make([]any, len(elems))
		for i, e := range elems {
			a, err := sqlrow.Arg(e)
			if err != nil {
				return nil, err
			}
			out[i] = a
		}
		return out, nil
	}
	return sqlrow.Arg(v)
}

// multiStatementHint explains Postgres's error for several statements
// in a prepared query.
func multiStatementHint(err error, args []any) string {
	var pgErr *pgconn.PgError
	if args != nil && errors.As(err, &pgErr) && strings.Contains(pgErr.Message, "multiple commands") {
		return "; a query with params must be a single statement"
	}
	return ""
}

// pgValue converts a value pgx decoded, handling the types sqlrow
// doesn't know: numeric as an exact number, uuid as its text, arrays
// as lists, json as its text, and other pgtype values by their
// driver value.
func pgValue(v any) cty.Value {
	switch val := v.(type) {
	case pgtype.Numeric:
		if !val.Valid {
			return cty.NullVal(cty.DynamicPseudoType)
		}
		dv, err := val.Value()
		s, _ := dv.(string)
		if n, perr := cty.ParseNumberVal(s); err == nil && perr == nil {
			return n
		}
		return cty.StringVal(s) // NaN, Infinity
	case [16]byte:
		return cty.StringVal(fmt.Sprintf("%x-%x-%x-%x-%x", val[0:4], val[4:6], val[6:8], val[8:10], val[10:16]))
	case []any:
		if len(val) == 0 {
			return cty.EmptyTupleVal
		}
		elems := make([]cty.Value, len(val))
		for i, e := range val {
			elems[i] = pgValue(e)
		}
		return cty.TupleVal(elems)
	case map[string]any:
		b, err := json.Marshal(val)
		if err != nil {
			return cty.StringVal(fmt.Sprintf("%v", val))
		}
		return cty.StringVal(string(b))
	case driver.Valuer:
		dv, err := val.Value()
		if err != nil {
			return cty.StringVal(fmt.Sprintf("%v", val))
		}
		return sqlrow.ToCty(dv)
	}
	return sqlrow.ToCty(v)
}

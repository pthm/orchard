// Package sqlrow turns the result of a SQL statement into action
// outputs for Orchard's database providers.
//
// The database/sql providers (sqlite, mysql) share Query, which gives
// every SQL action the same output shape: the first returned row, or
// last_insert_id and rows_affected for a statement that returns no
// rows. ToCty is shared with the postgres provider, which uses pgx
// directly.
package sqlrow

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"
)

// Query runs query on conn and returns its outputs:
//
//   - If the statement returns columns (a SELECT, or a write with
//     RETURNING), the first row keyed by column name. No rows → empty.
//   - Otherwise (a plain INSERT, UPDATE, DELETE or DDL), the
//     last_insert_id and rows_affected that statusQuery reports. It
//     runs on the same connection straight after the statement, and
//     must select those two values as integers, in that order.
//
// Only the first row is read. Callers' databases must apply a
// statement's writes before its first row is returned, which SQLite
// and MySQL both do.
func Query(ctx context.Context, conn *sql.Conn, query, statusQuery string) (map[string]cty.Value, error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}
	cols, err := rows.ColumnTypes()
	if err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("columns: %w", err)
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
			return nil, fmt.Errorf("read row: %w", err)
		}
		for i, col := range cols {
			outputs[col.Name()] = ColumnValue(values[i], col.DatabaseTypeName())
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration: %w", err)
	}

	if len(cols) == 0 {
		var id, affected int64
		if err := conn.QueryRowContext(ctx, statusQuery).Scan(&id, &affected); err != nil {
			return nil, fmt.Errorf("read insert id: %w", err)
		}
		outputs["last_insert_id"] = cty.NumberIntVal(id)
		outputs["rows_affected"] = cty.NumberIntVal(affected)
	}
	return outputs, nil
}

// ColumnValue converts one scanned value. Some drivers return numbers
// as text: MySQL's text protocol (queries without bind parameters) as
// []byte, and pgx's database/sql adapter returns NUMERIC as a string.
// Those are decoded by the column's declared type; everything else
// goes through ToCty.
func ColumnValue(v any, dbType string) cty.Value {
	if isNumericType(dbType) {
		var text string
		switch val := v.(type) {
		case []byte:
			text = string(val)
		case string:
			text = val
		default:
			return ToCty(v)
		}
		if n, err := cty.ParseNumberVal(text); err == nil {
			return n
		}
	}
	return ToCty(v)
}

// isNumericType reports whether a driver's column type name (as from
// sql.ColumnType.DatabaseTypeName) holds a number. Names are matched
// exactly, so POINT or INTERVAL don't pass for INT.
func isNumericType(name string) bool {
	switch strings.TrimPrefix(strings.ToUpper(name), "UNSIGNED ") {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT",
		"DECIMAL", "NUMERIC", "FLOAT", "DOUBLE", "REAL",
		"INT2", "INT4", "INT8", "FLOAT4", "FLOAT8": // Postgres spellings
		return true
	}
	return false
}

// ToCty converts a Go value from a database driver into a cty.Value
// for the engine's eval context. Drivers return their native types
// (strings, sized ints, floats, bool, []byte, time.Time, nil); those
// map directly, times become RFC 3339 strings, and anything else falls
// back to its string form.
//
// This is deliberately permissive rather than strict: a best-effort
// string conversion is a better failure mode during scenario
// development than a hard error on an unhandled type. Columns the
// scenario actually references surface decoding issues at
// expression-evaluation time, which is a more actionable place to see
// them.
func ToCty(v any) cty.Value {
	switch val := v.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	case string:
		return cty.StringVal(val)
	case []byte:
		return cty.StringVal(string(val))
	case bool:
		return cty.BoolVal(val)
	case int:
		return cty.NumberIntVal(int64(val))
	case int16:
		return cty.NumberIntVal(int64(val))
	case int32:
		return cty.NumberIntVal(int64(val))
	case int64:
		return cty.NumberIntVal(val)
	case uint64:
		return cty.NumberUIntVal(val)
	case float32:
		return cty.NumberFloatVal(float64(val))
	case float64:
		return cty.NumberFloatVal(val)
	case *big.Float:
		f, _ := val.Float64()
		return cty.NumberFloatVal(f)
	case time.Time:
		return cty.StringVal(val.Format(time.RFC3339Nano))
	default:
		return cty.StringVal(fmt.Sprintf("%v", val))
	}
}

// Package sqlcrud runs the typed insert, update and delete actions for
// Orchard's SQL providers over database/sql. Values are always bound as
// parameters, converted by the target column's kind; identifiers are
// quoted per dialect. Callers validate with pkg/dbschema first.
package sqlcrud

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/pthm/orchard/internal/sqlrow"
	"github.com/pthm/orchard/pkg/dbschema"
)

// Dialect is how one database spells the statements.
type Dialect struct {
	// Placeholder returns the nth (1-based) bind parameter.
	Placeholder func(n int) string
	// Quote quotes an identifier.
	Quote func(ident string) string
	// Returning is true when INSERT ... RETURNING * gives the new row.
	// Otherwise Insert reads it back by primary key.
	Returning bool
}

// The three built-in dialects.
var (
	Postgres = Dialect{Placeholder: dollar, Quote: doubleQuote, Returning: true}
	SQLite   = Dialect{Placeholder: question, Quote: doubleQuote, Returning: true}
	MySQL    = Dialect{Placeholder: question, Quote: backtick, Returning: false}
)

func dollar(n int) string { return "$" + strconv.Itoa(n) }
func question(int) string { return "?" }
func doubleQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func backtick(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// Insert inserts one row into t and returns the full row, every column
// decoded by kind. values is a known object of column = value.
func Insert(ctx context.Context, conn *sql.Conn, d Dialect, t *dbschema.Table, values cty.Value) (map[string]cty.Value, error) {
	fields := sortedFields(values)
	cols := make([]string, len(fields))
	ph := make([]string, len(fields))
	args := make([]any, len(fields))
	for i, f := range fields {
		c, err := column(t, f.name)
		if err != nil {
			return nil, err
		}
		if args[i], err = bind(c, f.value); err != nil {
			return nil, err
		}
		cols[i] = d.Quote(f.name)
		ph[i] = d.Placeholder(i + 1)
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", tableRef(d, t), strings.Join(cols, ", "), strings.Join(ph, ", "))

	if d.Returning {
		rows, err := conn.QueryContext(ctx, q+" RETURNING *", args...)
		if err != nil {
			return nil, err
		}
		return readRow(t, rows)
	}

	res, err := conn.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return readBack(ctx, conn, d, t, values, res)
}

// readBack reads an inserted row by primary key, for dialects without
// RETURNING. A key column not in values is taken from the insert's
// auto-increment id (only possible for a single-column key). A table
// without a primary key returns the values as given, plus
// last_insert_id.
func readBack(ctx context.Context, conn *sql.Conn, d Dialect, t *dbschema.Table, values cty.Value, res sql.Result) (map[string]cty.Value, error) {
	given := values.AsValueMap()
	if len(t.PrimaryKey) == 0 {
		out := make(map[string]cty.Value, len(given)+1)
		for k, v := range given {
			out[k] = v
		}
		if id, err := res.LastInsertId(); err == nil {
			out["last_insert_id"] = cty.NumberIntVal(id)
		}
		return out, nil
	}

	where := make([]string, len(t.PrimaryKey))
	args := make([]any, len(t.PrimaryKey))
	for i, name := range t.PrimaryKey {
		where[i] = d.Quote(name) + " = " + d.Placeholder(i+1)
		if v, ok := given[name]; ok {
			c, err := column(t, name)
			if err != nil {
				return nil, err
			}
			if args[i], err = bind(c, v); err != nil {
				return nil, err
			}
			continue
		}
		if len(t.PrimaryKey) > 1 {
			return nil, fmt.Errorf("can't read the row back: primary key column %q wasn't set", name)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("can't read the row back: %w", err)
		}
		args[i] = id
	}
	q := fmt.Sprintf("SELECT * FROM %s WHERE %s", tableRef(d, t), strings.Join(where, " AND "))
	rows, err := conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return readRow(t, rows)
}

// Update sets columns on the rows of t matching where, and returns
// rows_affected.
func Update(ctx context.Context, conn *sql.Conn, d Dialect, t *dbschema.Table, set, where cty.Value) (map[string]cty.Value, error) {
	var args []any
	var assigns []string
	for _, f := range sortedFields(set) {
		c, err := column(t, f.name)
		if err != nil {
			return nil, err
		}
		arg, err := bind(c, f.value)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		assigns = append(assigns, d.Quote(f.name)+" = "+d.Placeholder(len(args)))
	}
	cond, args, err := whereClause(d, t, where, args)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf("UPDATE %s SET %s WHERE %s", tableRef(d, t), strings.Join(assigns, ", "), cond)
	return exec(ctx, conn, q, args)
}

// Delete deletes the rows of t matching where, and returns
// rows_affected.
func Delete(ctx context.Context, conn *sql.Conn, d Dialect, t *dbschema.Table, where cty.Value) (map[string]cty.Value, error) {
	cond, args, err := whereClause(d, t, where, nil)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf("DELETE FROM %s WHERE %s", tableRef(d, t), cond)
	return exec(ctx, conn, q, args)
}

func exec(ctx context.Context, conn *sql.Conn, q string, args []any) (map[string]cty.Value, error) {
	res, err := conn.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	return map[string]cty.Value{"rows_affected": cty.NumberIntVal(n)}, nil
}

// whereClause builds "a = $n AND b IS NULL" from where, continuing the
// placeholder numbering after args.
func whereClause(d Dialect, t *dbschema.Table, where cty.Value, args []any) (cond string, allArgs []any, err error) {
	var conds []string
	for _, f := range sortedFields(where) {
		c, err := column(t, f.name)
		if err != nil {
			return "", nil, err
		}
		if f.value.IsNull() {
			conds = append(conds, d.Quote(f.name)+" IS NULL")
			continue
		}
		arg, err := bind(c, f.value)
		if err != nil {
			return "", nil, err
		}
		args = append(args, arg)
		conds = append(conds, d.Quote(f.name)+" = "+d.Placeholder(len(args)))
	}
	if len(conds) == 0 {
		return "", nil, fmt.Errorf("where names no columns")
	}
	return strings.Join(conds, " AND "), args, nil
}

func tableRef(d Dialect, t *dbschema.Table) string {
	if t.Schema == "" {
		return d.Quote(t.Name)
	}
	return d.Quote(t.Schema) + "." + d.Quote(t.Name)
}

func column(t *dbschema.Table, name string) (*dbschema.Column, error) {
	c, ok := t.Column(name)
	if !ok {
		return nil, fmt.Errorf("table %q has no column %q", t.Key(), name)
	}
	return c, nil
}

type field struct {
	name  string
	value cty.Value
}

// sortedFields returns an object's fields in name order, so statements
// are deterministic.
func sortedFields(obj cty.Value) []field {
	m := obj.AsValueMap()
	out := make([]field, 0, len(m))
	for k, v := range m {
		out = append(out, field{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// bind converts a value to the driver argument for column c, by kind.
// Decimals go as exact text and JSON as encoded text, so no precision
// or structure is lost on the way.
func bind(c *dbschema.Column, v cty.Value) (any, error) {
	if !v.IsKnown() {
		return nil, fmt.Errorf("column %q: value isn't known", c.Name)
	}
	if v.IsNull() {
		return nil, nil
	}
	fail := func(err error) (any, error) {
		return nil, fmt.Errorf("column %q (%s): %w", c.Name, c.Kind, err)
	}
	switch c.Kind {
	case dbschema.KindInteger:
		n, err := convert.Convert(v, cty.Number)
		if err != nil {
			return fail(err)
		}
		if i, acc := n.AsBigFloat().Int64(); acc == 0 {
			return i, nil
		}
		return n.AsBigFloat().Text('f', -1), nil // beyond int64: let the database parse it
	case dbschema.KindDecimal:
		n, err := convert.Convert(v, cty.Number)
		if err != nil {
			return fail(err)
		}
		return n.AsBigFloat().Text('f', -1), nil
	case dbschema.KindFloat:
		n, err := convert.Convert(v, cty.Number)
		if err != nil {
			return fail(err)
		}
		f, _ := n.AsBigFloat().Float64()
		return f, nil
	case dbschema.KindBoolean:
		b, err := convert.Convert(v, cty.Bool)
		if err != nil {
			return fail(err)
		}
		return b.True(), nil
	case dbschema.KindBinary:
		s, err := convert.Convert(v, cty.String)
		if err != nil {
			return fail(err)
		}
		return []byte(s.AsString()), nil
	case dbschema.KindJSON, dbschema.KindOther:
		return bindLoose(v)
	default: // text, uuid, enum, date, time, timestamp
		s, err := convert.Convert(v, cty.String)
		if err != nil {
			return fail(err)
		}
		return s.AsString(), nil
	}
}

// bindLoose passes primitives through as their natural Go type and
// encodes collections as JSON text.
func bindLoose(v cty.Value) (any, error) {
	switch {
	case v.Type() == cty.String:
		return v.AsString(), nil
	case v.Type() == cty.Bool:
		return v.True(), nil
	case v.Type() == cty.Number:
		return v.AsBigFloat().Text('f', -1), nil
	default:
		b, err := ctyjson.Marshal(v, v.Type())
		if err != nil {
			return nil, err
		}
		return string(b), nil
	}
}

// readRow reads the first row of rows, every column decoded by the
// table's kinds where the column is known.
func readRow(t *dbschema.Table, rows *sql.Rows) (map[string]cty.Value, error) {
	defer func() { _ = rows.Close() }()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("the inserted row couldn't be read back")
	}
	raw := make([]any, len(types))
	ptrs := make([]any, len(types))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]cty.Value, len(types))
	for i, ct := range types {
		c, _ := t.Column(ct.Name())
		out[ct.Name()] = decode(c, raw[i], ct.DatabaseTypeName())
	}
	return out, rows.Err()
}

// decode converts a scanned value, using the column's kind to fix up
// what drivers return generically: MySQL booleans are 0/1, JSON comes
// back as text, and SQLite may store numbers as text.
func decode(c *dbschema.Column, raw any, dbType string) cty.Value {
	v := sqlrow.ColumnValue(raw, dbType)
	if c == nil || v.IsNull() {
		return v
	}
	switch c.Kind {
	case dbschema.KindBoolean:
		if v.Type() == cty.Number {
			return cty.BoolVal(v.AsBigFloat().Sign() != 0)
		}
	case dbschema.KindInteger, dbschema.KindDecimal, dbschema.KindFloat:
		if v.Type() == cty.String {
			if n, err := cty.ParseNumberVal(v.AsString()); err == nil {
				return n
			}
		}
	case dbschema.KindJSON:
		if v.Type() == cty.String {
			data := []byte(v.AsString())
			if ty, err := ctyjson.ImpliedType(data); err == nil {
				if decoded, err := ctyjson.Unmarshal(data, ty); err == nil {
					return decoded
				}
			}
		}
	}
	return v
}

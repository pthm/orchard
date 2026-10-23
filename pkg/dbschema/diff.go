package dbschema

import (
	"fmt"
	"slices"
	"strings"
)

// Diff lists how cur differs from old, one human-readable line per
// change: tables and columns added or removed, and changes to a
// column's type, nullability, default, generated status or enum values,
// or to a table's primary or foreign keys. Comments are ignored; they
// don't affect what an action may do. The result is sorted by table and
// empty when the schemas match.
func Diff(old, cur *Schema) []string {
	var out []string
	for _, key := range union(old.TableNames(), cur.TableNames()) {
		ot, inOld := old.Tables[key]
		ct, inCur := cur.Tables[key]
		switch {
		case !inOld:
			out = append(out, fmt.Sprintf("table %q added", key))
		case !inCur:
			out = append(out, fmt.Sprintf("table %q removed", key))
		default:
			out = append(out, diffTable(key, ot, ct)...)
		}
	}
	return out
}

func diffTable(key string, old, cur *Table) []string {
	var out []string
	for _, name := range union(columnNames(old), columnNames(cur)) {
		oc, inOld := old.Column(name)
		cc, inCur := cur.Column(name)
		col := fmt.Sprintf("column %q", key+"."+name)
		switch {
		case !inOld:
			out = append(out, col+" added")
		case !inCur:
			out = append(out, col+" removed")
		default:
			if changes := diffColumn(oc, cc); len(changes) > 0 {
				out = append(out, col+": "+strings.Join(changes, "; "))
			}
		}
	}
	if !slices.Equal(old.PrimaryKey, cur.PrimaryKey) {
		out = append(out, fmt.Sprintf("primary key of %q: (%s) → (%s)", key,
			strings.Join(old.PrimaryKey, ", "), strings.Join(cur.PrimaryKey, ", ")))
	}
	if !slices.EqualFunc(old.UniqueKeys, cur.UniqueKeys, slices.Equal[[]string]) {
		out = append(out, fmt.Sprintf("unique keys of %q changed", key))
	}
	if !slices.EqualFunc(old.ForeignKeys, cur.ForeignKeys, func(a, b ForeignKey) bool {
		return a.References == b.References && slices.Equal(a.Columns, b.Columns) &&
			slices.Equal(a.ReferencedColumns, b.ReferencedColumns)
	}) {
		out = append(out, fmt.Sprintf("foreign keys of %q changed", key))
	}
	return out
}

func diffColumn(old, cur *Column) []string {
	var out []string
	if old.Type != cur.Type {
		out = append(out, fmt.Sprintf("type %s → %s", old.Type, cur.Type))
	}
	if old.Nullable != cur.Nullable {
		out = append(out, map[bool]string{true: "now nullable", false: "now NOT NULL"}[cur.Nullable])
	}
	if old.HasDefault != cur.HasDefault {
		out = append(out, map[bool]string{true: "default added", false: "default removed"}[cur.HasDefault])
	}
	if old.Generated != cur.Generated {
		out = append(out, fmt.Sprintf("generated %s → %s", orNone(old.Generated), orNone(cur.Generated)))
	}
	if !slices.Equal(old.Enum, cur.Enum) {
		out = append(out, fmt.Sprintf("enum values (%s) → (%s)", strings.Join(old.Enum, ", "), strings.Join(cur.Enum, ", ")))
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// union returns the sorted union of two sorted-or-not name lists.
func union(a, b []string) []string {
	out := append(slices.Clone(a), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

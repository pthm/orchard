package dbschema

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// ErrNoTable is wrapped by Lookup's error for a table that doesn't
// exist, so callers can tell it apart from other failures.
var ErrNoTable = errors.New("no such table")

// Lookup returns the table named by name: "table" or "schema.table".
// An unqualified name resolves against DefaultSchema when the database
// has namespaces. The error suggests a close match when there is one.
func (s *Schema) Lookup(name string) (*Table, error) {
	if t, ok := s.Tables[name]; ok {
		return t, nil
	}
	if s.DefaultSchema != "" && !strings.Contains(name, ".") {
		if t, ok := s.Tables[TableKey(s.DefaultSchema, name)]; ok {
			return t, nil
		}
	}
	// Suggest in the form the name was written: unqualified names
	// against the default schema's tables without their prefix.
	candidates := s.TableNames()
	if s.DefaultSchema != "" && !strings.Contains(name, ".") {
		for i, c := range candidates {
			candidates[i] = strings.TrimPrefix(c, s.DefaultSchema+".")
		}
	}
	return nil, fmt.Errorf("%w %q%s", ErrNoTable, name, suggest(name, candidates))
}

// Column returns the named column.
func (t *Table) Column(name string) (*Column, bool) {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i], true
		}
	}
	return nil, false
}

// Key returns the table's key in Schema.Tables.
func (t *Table) Key() string { return TableKey(t.Schema, t.Name) }

// CheckInsert validates the values of an insert into t: every column
// exists, isn't generated, and takes its value; every required column
// (not nullable, no default, not generated) is set. values is an
// object or map and may contain unknown values (at plan time); those
// are checked by type when their type is known.
func CheckInsert(t *Table, values cty.Value) error {
	var errs []error
	present, err := checkColumnValues(t, "values", values, true, &errs)
	if err != nil {
		return err
	}
	if present != nil {
		for _, c := range t.Columns {
			if !c.Nullable && !c.HasDefault && c.Generated == "" && !present[c.Name] {
				errs = append(errs, fmt.Errorf("missing required column %q", c.Name))
			}
		}
	}
	return joinErrs(t, errs)
}

// CheckUpdate validates an update of t: set names writable columns and
// takes their values, and where names existing columns.
func CheckUpdate(t *Table, set, where cty.Value) error {
	var errs []error
	if _, err := checkColumnValues(t, "set", set, true, &errs); err != nil {
		return err
	}
	if _, err := checkColumnValues(t, "where", where, false, &errs); err != nil {
		return err
	}
	return joinErrs(t, errs)
}

// CheckDelete validates a delete from t: where names existing columns.
func CheckDelete(t *Table, where cty.Value) error {
	var errs []error
	if _, err := checkColumnValues(t, "where", where, false, &errs); err != nil {
		return err
	}
	return joinErrs(t, errs)
}

// checkColumnValues checks one column → value object, appending each
// problem to errs. writing is true for values/set (generated columns
// are rejected) and false for where. It returns the column names set,
// or nil when they can't be known yet (the whole object is unknown and
// untyped). A non-object value or an empty object is returned as err.
func checkColumnValues(t *Table, attr string, obj cty.Value, writing bool, errs *[]error) (map[string]bool, error) {
	if obj.IsNull() {
		return nil, fmt.Errorf("%s must not be null", attr)
	}
	ty := obj.Type()
	if ty == cty.DynamicPseudoType {
		return nil, nil // unknown until run time
	}
	fields, err := objectFields(obj)
	if err != nil {
		return nil, fmt.Errorf("%s must be an object of column = value, got %s", attr, ty.FriendlyName())
	}
	if fields == nil {
		return nil, nil // a map whose keys aren't known yet
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%s names no columns", attr)
	}

	present := make(map[string]bool, len(fields))
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		present[name] = true
		c, ok := t.Column(name)
		if !ok {
			*errs = append(*errs, fmt.Errorf("%s: no column %q%s", attr, name, suggest(name, columnNames(t))))
			continue
		}
		if writing && c.Generated != "" {
			*errs = append(*errs, fmt.Errorf("%s: column %q is generated and can't be set", attr, name))
			continue
		}
		if err := CheckValue(c, fields[name]); err != nil {
			*errs = append(*errs, fmt.Errorf("%s: %w", attr, err))
		}
	}
	return present, nil
}

// objectFields returns an object's or map's fields. For an unknown
// object, the fields are unknown values of the attribute types; for an
// unknown map, nil (keys unknown).
func objectFields(v cty.Value) (map[string]cty.Value, error) {
	ty := v.Type()
	switch {
	case ty.IsObjectType():
		if !v.IsKnown() {
			out := map[string]cty.Value{}
			for name, at := range ty.AttributeTypes() {
				out[name] = cty.UnknownVal(at)
			}
			return out, nil
		}
		return valueMap(v), nil
	case ty.IsMapType():
		if !v.IsKnown() {
			return nil, nil
		}
		return valueMap(v), nil
	default:
		return nil, fmt.Errorf("not an object")
	}
}

// valueMap is AsValueMap, but empty rather than nil for an empty
// collection, so callers can tell "no fields" from "unknown fields".
func valueMap(v cty.Value) map[string]cty.Value {
	if m := v.AsValueMap(); m != nil {
		return m
	}
	return map[string]cty.Value{}
}

// CheckValue reports whether v can be stored in column c: null only in
// a nullable column, and otherwise a value that converts to the
// column kind's type (so "5" from a string variable fits an integer
// column), integers whole, and enums one of their members. Unknown
// values are checked by type only; unknown types aren't checked.
func CheckValue(c *Column, v cty.Value) error {
	if v.Type() == cty.DynamicPseudoType && !v.IsKnown() {
		return nil
	}
	if v.IsKnown() && v.IsNull() {
		if !c.Nullable {
			return fmt.Errorf("column %q is NOT NULL; got null", c.Name)
		}
		return nil
	}
	target := KindType(c.Kind)
	if target == cty.DynamicPseudoType {
		return nil
	}
	if !v.IsKnown() {
		// GetConversionUnsafe returns nil both for impossible and for
		// unneeded conversions, so check equal types first.
		if !v.Type().Equals(target) && convert.GetConversionUnsafe(v.Type(), target) == nil {
			return mismatch(c, v)
		}
		return nil
	}
	converted, err := convert.Convert(v, target)
	if err != nil {
		return mismatch(c, v)
	}
	switch c.Kind {
	case KindInteger:
		if !converted.AsBigFloat().IsInt() {
			return fmt.Errorf("column %q is integer; got %s", c.Name, converted.AsBigFloat().Text('g', -1))
		}
	case KindEnum:
		if len(c.Enum) > 0 && !contains(c.Enum, converted.AsString()) {
			return fmt.Errorf("column %q is enum (%s); got %q", c.Name, strings.Join(c.Enum, ", "), converted.AsString())
		}
	}
	return nil
}

// KindType is the cty type values of a column kind must convert to.
// JSON and other kinds accept anything (cty.DynamicPseudoType).
func KindType(k Kind) cty.Type {
	switch k {
	case KindInteger, KindDecimal, KindFloat:
		return cty.Number
	case KindBoolean:
		return cty.Bool
	case KindText, KindUUID, KindEnum, KindDate, KindTime, KindTimestamp, KindBinary:
		return cty.String
	default:
		return cty.DynamicPseudoType
	}
}

func mismatch(c *Column, v cty.Value) error {
	return fmt.Errorf("column %q is %s; got %s", c.Name, c.Kind, v.Type().FriendlyName())
}

func joinErrs(t *Table, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return fmt.Errorf("table %q: %w", t.Key(), errs[0])
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = "  - " + e.Error()
	}
	return fmt.Errorf("table %q:\n%s", t.Key(), strings.Join(msgs, "\n"))
}

func columnNames(t *Table) []string {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = c.Name
	}
	return names
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// suggest returns ` (did you mean "x"?)` for the closest candidate
// within a small edit distance, or "".
func suggest(name string, candidates []string) string {
	best, bestDist := "", 0
	for _, c := range candidates {
		d := editDistance(strings.ToLower(name), strings.ToLower(c))
		if best == "" || d < bestDist {
			best, bestDist = c, d
		}
	}
	if best == "" || bestDist > max(2, len(name)/3) {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

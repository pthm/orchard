// Package dbschema is the portable model of a relational database's
// shape: tables, columns, keys, and enums. Orchard's SQL providers
// produce it by reading their database's catalog, `orchard schema
// pull` stores it as the body of a "database" snapshot, and plan-time
// checks (and editor tooling) read it back.
//
// The model is database-neutral. Each column keeps its native type as
// the database spells it (varchar(100), numeric(10,2)) alongside a
// portable Kind, so checks can reason about kinds while tooling can
// show the real type.
package dbschema

import "sort"

// SnapshotKind is the snapshot `kind` for a database schema body.
const SnapshotKind = "database"

// Kind is a column's portable type family.
type Kind string

// Column kinds. Each SQL provider maps its native types onto these.
const (
	KindInteger   Kind = "integer"
	KindDecimal   Kind = "decimal"
	KindFloat     Kind = "float"
	KindBoolean   Kind = "boolean"
	KindText      Kind = "text"
	KindUUID      Kind = "uuid"
	KindEnum      Kind = "enum"
	KindDate      Kind = "date"
	KindTime      Kind = "time"
	KindTimestamp Kind = "timestamp"
	KindJSON      Kind = "json"
	KindBinary    Kind = "binary"
	// KindOther covers types with no portable kind (arrays, intervals,
	// geometry, ...). Values for them aren't type-checked.
	KindOther Kind = "other"
)

// Generated values for Column.Generated. Columns with any of these
// can't be set by an insert or update.
const (
	// GeneratedIdentity is an identity column that always generates
	// its value (Postgres GENERATED ALWAYS AS IDENTITY).
	GeneratedIdentity = "identity"
	// GeneratedStored and GeneratedVirtual are computed columns.
	GeneratedStored  = "stored"
	GeneratedVirtual = "virtual"
)

// Schema is one database's tables.
type Schema struct {
	// Dialect is the database family: "postgres", "mysql" or "sqlite".
	Dialect string `json:"dialect"`
	// Database is the database name (for SQLite, the file name). It
	// never includes a host, user or password.
	Database string `json:"database"`
	// DefaultSchema is the namespace unqualified table names resolve
	// against (Postgres's current_schema()). Empty for databases with
	// a single namespace.
	DefaultSchema string `json:"default_schema,omitempty"`
	// Tables is keyed by TableKey.
	Tables map[string]*Table `json:"tables"`
}

// Table is one table.
type Table struct {
	// Schema is the table's namespace, empty for databases with one.
	Schema  string `json:"schema,omitempty"`
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
	// Columns are in the table's declared order.
	Columns     []Column     `json:"columns"`
	PrimaryKey  []string     `json:"primary_key,omitempty"`
	ForeignKeys []ForeignKey `json:"foreign_keys,omitempty"`
}

// Column is one column.
type Column struct {
	Name string `json:"name"`
	// Type is the native type as the database spells it.
	Type     string `json:"type"`
	Kind     Kind   `json:"kind"`
	Nullable bool   `json:"nullable"`
	// HasDefault is true when the database supplies a value if the
	// column is omitted: a DEFAULT, a sequence, AUTO_INCREMENT, an
	// identity, or SQLite's rowid.
	HasDefault bool `json:"has_default,omitempty"`
	// Generated is one of the Generated* constants, or empty.
	Generated string `json:"generated,omitempty"`
	// Enum lists the allowed values of an enum column, in order.
	Enum    []string `json:"enum,omitempty"`
	Comment string   `json:"comment,omitempty"`
}

// ForeignKey is a foreign key from Columns to ReferencedColumns of
// the table named by References (a TableKey).
type ForeignKey struct {
	Columns           []string `json:"columns"`
	References        string   `json:"references"`
	ReferencedColumns []string `json:"referenced_columns"`
}

// TableKey is the key a table is stored under in Schema.Tables:
// "schema.name" for databases with namespaces, otherwise "name".
func TableKey(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// TableNames returns the keys of s.Tables in sorted order.
func (s *Schema) TableNames() []string {
	names := make([]string, 0, len(s.Tables))
	for name := range s.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

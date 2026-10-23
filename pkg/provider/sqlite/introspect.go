package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/provider"
)

// Compile-time assertion that Provider can be snapshotted.
var _ provider.SchemaSource = (*Provider)(nil)

// PullSchema captures the database's tables (not views). SQLite has
// one namespace, so opts.Schemas is ignored.
func (p *Provider) PullSchema(ctx context.Context, _ provider.PullOptions) (*provider.Capture, error) {
	if p.db == nil {
		return nil, fmt.Errorf("sqlite: not configured")
	}
	s, err := p.readSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read schema: %w", err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return &provider.Capture{Kind: dbschema.SnapshotKind, Target: "sqlite/" + s.Database, Body: body}, nil
}

// readSchema reads the live schema on a connection of its own.
func (p *Provider) readSchema(ctx context.Context) (*dbschema.Schema, error) {
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	return introspect(ctx, conn, p.path)
}

// introspect reads every table's columns, primary key and foreign
// keys. path is the configured database path; only its base name is
// recorded, since the full path is machine-specific.
func introspect(ctx context.Context, conn *sql.Conn, path string) (*dbschema.Schema, error) {
	s := &dbschema.Schema{
		Dialect:  "sqlite",
		Database: filepath.Base(path),
		Tables:   map[string]*dbschema.Table{},
	}

	names, err := tableNames(ctx, conn)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		t, err := readTable(ctx, conn, name.name, name.withoutRowid)
		if err != nil {
			return nil, fmt.Errorf("table %q: %w", name.name, err)
		}
		s.Tables[name.name] = t
	}

	// A foreign key may omit its referenced columns, meaning the
	// referenced table's primary key; fill those in now that every
	// table is loaded.
	for _, t := range s.Tables {
		for i := range t.ForeignKeys {
			fk := &t.ForeignKeys[i]
			if len(fk.ReferencedColumns) == 0 {
				if ref, ok := s.Tables[fk.References]; ok {
					fk.ReferencedColumns = append([]string(nil), ref.PrimaryKey...)
				}
			}
		}
	}
	return s, nil
}

type tableName struct {
	name         string
	withoutRowid bool
}

func tableNames(ctx context.Context, conn *sql.Conn) ([]tableName, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT name, wr FROM pragma_table_list
		WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []tableName
	for rows.Next() {
		var t tableName
		if err := rows.Scan(&t.name, &t.withoutRowid); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func readTable(ctx context.Context, conn *sql.Conn, name string, withoutRowid bool) (*dbschema.Table, error) {
	t := &dbschema.Table{Name: name}

	rows, err := conn.QueryContext(ctx,
		`SELECT name, type, "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?) ORDER BY cid`, name)
	if err != nil {
		return nil, err
	}
	pkOrder := map[int]string{}
	for rows.Next() {
		var (
			col          dbschema.Column
			notNull      bool
			dflt         sql.NullString
			pk, hidden   int
			declaredType string
		)
		if err := rows.Scan(&col.Name, &declaredType, &notNull, &dflt, &pk, &hidden); err != nil {
			_ = rows.Close()
			return nil, err
		}
		switch hidden {
		case 1:
			continue // a virtual table's hidden column, not part of the table's shape
		case 2:
			col.Generated = dbschema.GeneratedVirtual
		case 3:
			col.Generated = dbschema.GeneratedStored
		}
		col.Type = declaredType
		col.Kind = kindOf(declaredType)
		col.Nullable = !notNull && pk == 0
		col.HasDefault = dflt.Valid
		if pk > 0 {
			pkOrder[pk] = col.Name
		}
		t.Columns = append(t.Columns, col)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := 1; i <= len(pkOrder); i++ {
		t.PrimaryKey = append(t.PrimaryKey, pkOrder[i])
	}

	// A single INTEGER PRIMARY KEY is the rowid: SQLite assigns it
	// when it's omitted.
	if !withoutRowid && len(t.PrimaryKey) == 1 {
		for i := range t.Columns {
			c := &t.Columns[i]
			if c.Name == t.PrimaryKey[0] && strings.EqualFold(c.Type, "INTEGER") {
				c.HasDefault = true
			}
		}
	}

	fks, err := conn.QueryContext(ctx,
		`SELECT id, "table", "from", "to" FROM pragma_foreign_key_list(?) ORDER BY id, seq`, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fks.Close() }()
	byID := map[int]int{} // foreign key id → index in t.ForeignKeys
	for fks.Next() {
		var (
			id   int
			ref  string
			from string
			to   sql.NullString
		)
		if err := fks.Scan(&id, &ref, &from, &to); err != nil {
			return nil, err
		}
		i, ok := byID[id]
		if !ok {
			i = len(t.ForeignKeys)
			byID[id] = i
			t.ForeignKeys = append(t.ForeignKeys, dbschema.ForeignKey{References: ref})
		}
		fk := &t.ForeignKeys[i]
		fk.Columns = append(fk.Columns, from)
		if to.Valid {
			fk.ReferencedColumns = append(fk.ReferencedColumns, to.String)
		}
	}
	return t, fks.Err()
}

// kindOf maps a declared column type to a kind. Common explicit names
// (BOOLEAN, DATETIME, JSON, ...) are recognised first; anything else
// follows SQLite's type-affinity rules
// (https://www.sqlite.org/datatype3.html#determination_of_column_affinity).
func kindOf(declared string) dbschema.Kind {
	t := strings.ToUpper(strings.TrimSpace(declared))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	switch t {
	case "BOOL", "BOOLEAN":
		return dbschema.KindBoolean
	case "DATE":
		return dbschema.KindDate
	case "TIME":
		return dbschema.KindTime
	case "DATETIME", "TIMESTAMP":
		return dbschema.KindTimestamp
	case "JSON", "JSONB":
		return dbschema.KindJSON
	case "UUID":
		return dbschema.KindUUID
	case "DECIMAL", "NUMERIC":
		return dbschema.KindDecimal
	}
	switch {
	case strings.Contains(t, "INT"):
		return dbschema.KindInteger
	case strings.Contains(t, "CHAR"), strings.Contains(t, "CLOB"), strings.Contains(t, "TEXT"):
		return dbschema.KindText
	case t == "":
		return dbschema.KindOther // no declared type: any value goes
	case strings.Contains(t, "BLOB"):
		return dbschema.KindBinary
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUBLE"):
		return dbschema.KindFloat
	default:
		return dbschema.KindOther // NUMERIC affinity under another name
	}
}

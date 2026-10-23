package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/provider"
)

// Compile-time assertion that Provider can be snapshotted.
var _ provider.SchemaSource = (*Provider)(nil)

// PullSchema captures the base tables (not views) of the database the
// DSN connects to. MySQL databases are single namespaces, so
// opts.Schemas is ignored.
func (p *Provider) PullSchema(ctx context.Context, _ provider.PullOptions) (*provider.Capture, error) {
	if p.db == nil {
		return nil, fmt.Errorf("mysql: not configured")
	}
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("mysql: connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	s, err := introspect(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("mysql: read schema: %w", err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return &provider.Capture{Kind: dbschema.SnapshotKind, Target: "mysql/" + s.Database, Body: body}, nil
}

func introspect(ctx context.Context, conn *sql.Conn) (*dbschema.Schema, error) {
	s := &dbschema.Schema{Dialect: "mysql", Tables: map[string]*dbschema.Table{}}

	var database sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		return nil, err
	}
	if !database.Valid {
		return nil, fmt.Errorf("the dsn names no database")
	}
	s.Database = database.String

	rows, err := conn.QueryContext(ctx, `
		SELECT TABLE_NAME, TABLE_COMMENT FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t dbschema.Table
		if err := rows.Scan(&t.Name, &t.Comment); err != nil {
			_ = rows.Close()
			return nil, err
		}
		s.Tables[t.Name] = &t
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = conn.QueryContext(ctx, `
		SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE,
		       COLUMN_DEFAULT IS NOT NULL, EXTRA, COLUMN_COMMENT
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			table, dataType, nullable, extra string
			col                              dbschema.Column
		)
		if err := rows.Scan(&table, &col.Name, &col.Type, &dataType, &nullable, &col.HasDefault, &extra, &col.Comment); err != nil {
			_ = rows.Close()
			return nil, err
		}
		t, ok := s.Tables[table]
		if !ok {
			continue // a view's column
		}
		col.Kind = kindOf(dataType, col.Type)
		if col.Kind == dbschema.KindEnum {
			col.Enum = enumValues(col.Type)
		}
		col.Nullable = nullable == "YES"
		extra = strings.ToUpper(extra)
		switch {
		case strings.Contains(extra, "VIRTUAL GENERATED"):
			col.Generated = dbschema.GeneratedVirtual
		case strings.Contains(extra, "STORED GENERATED"):
			col.Generated = dbschema.GeneratedStored
		case strings.Contains(extra, "AUTO_INCREMENT"):
			col.HasDefault = true
		}
		t.Columns = append(t.Columns, col)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	// Primary keys (constraint name PRIMARY) and foreign keys (rows with
	// a referenced table). Unique keys appear here too and are skipped.
	rows, err = conn.QueryContext(ctx, `
		SELECT TABLE_NAME, CONSTRAINT_NAME, COLUMN_NAME,
		       REFERENCED_TABLE_SCHEMA, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, CONSTRAINT_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	type fkKey struct{ table, name string }
	fkIndex := map[fkKey]int{}
	for rows.Next() {
		var (
			table, constraint, column   string
			refSchema, refTable, refCol sql.NullString
		)
		if err := rows.Scan(&table, &constraint, &column, &refSchema, &refTable, &refCol); err != nil {
			return nil, err
		}
		t, ok := s.Tables[table]
		if !ok {
			continue
		}
		switch {
		case constraint == "PRIMARY":
			t.PrimaryKey = append(t.PrimaryKey, column)
		case refTable.Valid:
			key := fkKey{table, constraint}
			i, ok := fkIndex[key]
			if !ok {
				ref := refTable.String
				if refSchema.Valid && refSchema.String != s.Database {
					ref = refSchema.String + "." + ref
				}
				i = len(t.ForeignKeys)
				fkIndex[key] = i
				t.ForeignKeys = append(t.ForeignKeys, dbschema.ForeignKey{References: ref})
			}
			fk := &t.ForeignKeys[i]
			fk.Columns = append(fk.Columns, column)
			fk.ReferencedColumns = append(fk.ReferencedColumns, refCol.String)
		}
	}
	return s, rows.Err()
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Close(); err != nil {
		return err
	}
	return rows.Err()
}

// kindOf maps a column's DATA_TYPE (and, for tinyint, its COLUMN_TYPE)
// to a kind. MySQL's BOOL is tinyint(1).
func kindOf(dataType, columnType string) dbschema.Kind {
	switch strings.ToLower(dataType) {
	case "tinyint":
		if strings.HasPrefix(strings.ToLower(columnType), "tinyint(1)") {
			return dbschema.KindBoolean
		}
		return dbschema.KindInteger
	case "smallint", "mediumint", "int", "integer", "bigint", "year":
		return dbschema.KindInteger
	case "decimal", "numeric":
		return dbschema.KindDecimal
	case "float", "double", "real":
		return dbschema.KindFloat
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext":
		return dbschema.KindText
	case "enum":
		return dbschema.KindEnum
	case "date":
		return dbschema.KindDate
	case "time":
		return dbschema.KindTime
	case "datetime", "timestamp":
		return dbschema.KindTimestamp
	case "json":
		return dbschema.KindJSON
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return dbschema.KindBinary
	default:
		return dbschema.KindOther // set, bit, geometry, ...
	}
}

// enumValues parses the members out of a COLUMN_TYPE like
// enum('us','eu','it”s'), where a doubled quote is an escaped one.
func enumValues(columnType string) []string {
	start := strings.IndexByte(columnType, '(')
	end := strings.LastIndexByte(columnType, ')')
	if start < 0 || end <= start {
		return nil
	}
	body := columnType[start+1 : end]
	var (
		out     []string
		cur     strings.Builder
		inQuote bool
	)
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\'' && inQuote && i+1 < len(body) && body[i+1] == '\'':
			cur.WriteByte('\'')
			i++
		case c == '\'':
			if inQuote {
				out = append(out, cur.String())
				cur.Reset()
			}
			inQuote = !inQuote
		case inQuote:
			cur.WriteByte(c)
		}
	}
	return out
}

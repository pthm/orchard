package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/provider"
)

// Compile-time assertion that Provider can be snapshotted.
var _ provider.SchemaSource = (*Provider)(nil)

// PullSchema captures ordinary and partitioned tables (not views or
// partitions) in every non-system schema, or only opts.Schemas.
func (p *Provider) PullSchema(ctx context.Context, opts provider.PullOptions) (*provider.Capture, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("postgres: not configured")
	}
	s, err := p.introspect(ctx, opts.Schemas)
	if err != nil {
		return nil, fmt.Errorf("postgres: read schema: %w", err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return &provider.Capture{Kind: dbschema.SnapshotKind, Target: "postgres/" + s.Database, Body: body}, nil
}

func (p *Provider) introspect(ctx context.Context, schemas []string) (*dbschema.Schema, error) {
	s := &dbschema.Schema{Dialect: "postgres", Tables: map[string]*dbschema.Table{}}
	if err := p.pool.QueryRow(ctx, `SELECT current_database(), coalesce(current_schema(), '')`).
		Scan(&s.Database, &s.DefaultSchema); err != nil {
		return nil, err
	}
	if schemas == nil {
		schemas = []string{}
	}

	// Tables, keyed by oid for the column and constraint queries.
	byOID := map[uint32]*dbschema.Table{}
	rows, err := p.pool.Query(ctx, `
		SELECT c.oid, n.nspname, c.relname, coalesce(obj_description(c.oid, 'pg_class'), '')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p')
		  AND NOT c.relispartition
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'
		  AND (cardinality($1::text[]) = 0 OR n.nspname = ANY($1))`, schemas)
	if err != nil {
		return nil, err
	}
	var oids []uint32
	for rows.Next() {
		var (
			oid uint32
			t   dbschema.Table
		)
		if err := rows.Scan(&oid, &t.Schema, &t.Name, &t.Comment); err != nil {
			rows.Close()
			return nil, err
		}
		byOID[oid] = &t
		oids = append(oids, oid)
		s.Tables[dbschema.TableKey(t.Schema, t.Name)] = &t
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(oids) == 0 {
		return s, nil
	}

	enums, err := p.enumLabels(ctx)
	if err != nil {
		return nil, err
	}

	// Columns. A domain reports its base type's name for the kind, and
	// its own name as the native type.
	rows, err = p.pool.Query(ctx, `
		SELECT a.attrelid, a.attname, format_type(a.atttypid, a.atttypmod),
		       coalesce(bt.typname, t.typname), coalesce(bt.typtype, t.typtype)::text,
		       coalesce(bt.oid, t.oid), coalesce(bt.typcategory, t.typcategory)::text,
		       a.attnotnull, a.atthasdef, a.attidentity::text, a.attgenerated::text,
		       coalesce(col_description(a.attrelid, a.attnum), '')
		FROM pg_attribute a
		JOIN pg_type t ON t.oid = a.atttypid
		LEFT JOIN pg_type bt ON t.typtype = 'd' AND bt.oid = t.typbasetype
		WHERE a.attrelid = ANY($1) AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attrelid, a.attnum`, oids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			rel                           uint32
			col                           dbschema.Column
			typName, typType, typCategory string
			typOID                        uint32
			notNull, hasDef               bool
			identity, generated           string
		)
		if err := rows.Scan(&rel, &col.Name, &col.Type, &typName, &typType, &typOID, &typCategory,
			&notNull, &hasDef, &identity, &generated, &col.Comment); err != nil {
			rows.Close()
			return nil, err
		}
		col.Kind = kindOf(typName, typType, typCategory)
		if col.Kind == dbschema.KindEnum {
			col.Enum = enums[typOID]
		}
		col.Nullable = !notNull
		col.HasDefault = hasDef || identity != ""
		switch {
		case identity == "a":
			col.Generated = dbschema.GeneratedIdentity
		case generated == "s":
			col.Generated = dbschema.GeneratedStored
		case generated == "v":
			col.Generated = dbschema.GeneratedVirtual
		}
		byOID[rel].Columns = append(byOID[rel].Columns, col)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Primary and foreign keys, with column names in key order.
	rows, err = p.pool.Query(ctx, `
		SELECT con.conrelid, con.contype::text,
		       ARRAY(SELECT a.attname FROM unnest(con.conkey) WITH ORDINALITY k(num, ord)
		             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.num ORDER BY k.ord),
		       coalesce(fn.nspname, ''), coalesce(fc.relname, ''),
		       ARRAY(SELECT a.attname FROM unnest(con.confkey) WITH ORDINALITY k(num, ord)
		             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.num ORDER BY k.ord)
		FROM pg_constraint con
		LEFT JOIN pg_class fc ON fc.oid = con.confrelid
		LEFT JOIN pg_namespace fn ON fn.oid = fc.relnamespace
		WHERE con.contype IN ('p', 'f') AND con.conrelid = ANY($1)
		ORDER BY con.conrelid, con.conname`, oids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			rel                uint32
			typ, refNS, refRel string
			cols, refCols      []string
		)
		if err := rows.Scan(&rel, &typ, &cols, &refNS, &refRel, &refCols); err != nil {
			return nil, err
		}
		t := byOID[rel]
		if typ == "p" {
			t.PrimaryKey = cols
			continue
		}
		t.ForeignKeys = append(t.ForeignKeys, dbschema.ForeignKey{
			Columns:           cols,
			References:        dbschema.TableKey(refNS, refRel),
			ReferencedColumns: refCols,
		})
	}
	return s, rows.Err()
}

// enumLabels returns every enum type's labels in declared order,
// keyed by type oid.
func (p *Provider) enumLabels(ctx context.Context) (map[uint32][]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT enumtypid, enumlabel FROM pg_enum ORDER BY enumtypid, enumsortorder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32][]string{}
	for rows.Next() {
		var (
			oid   uint32
			label string
		)
		if err := rows.Scan(&oid, &label); err != nil {
			return nil, err
		}
		out[oid] = append(out[oid], label)
	}
	return out, rows.Err()
}

// kindOf maps a Postgres type (its base type, for a domain) to a kind.
func kindOf(typName, typType, typCategory string) dbschema.Kind {
	if typType == "e" {
		return dbschema.KindEnum
	}
	if typCategory == "A" {
		return dbschema.KindOther // arrays
	}
	switch typName {
	case "int2", "int4", "int8":
		return dbschema.KindInteger
	case "numeric":
		return dbschema.KindDecimal
	case "float4", "float8":
		return dbschema.KindFloat
	case "bool":
		return dbschema.KindBoolean
	case "text", "varchar", "bpchar", "name", "citext":
		return dbschema.KindText
	case "uuid":
		return dbschema.KindUUID
	case "date":
		return dbschema.KindDate
	case "time", "timetz":
		return dbschema.KindTime
	case "timestamp", "timestamptz":
		return dbschema.KindTimestamp
	case "json", "jsonb":
		return dbschema.KindJSON
	case "bytea":
		return dbschema.KindBinary
	default:
		return dbschema.KindOther
	}
}

package sqlcrud

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/snapshot"
)

// Actions runs one provider's typed actions (<prefix>_insert, _update,
// _delete) against the live database: it reads the schema once per
// run, validates each action against it, then executes.
type Actions struct {
	// Prefix is the provider's action prefix, e.g. "postgres".
	Prefix  string
	Dialect Dialect
	// Conn returns a connection for one statement.
	Conn func(ctx context.Context) (*sql.Conn, error)
	// Introspect reads the live schema. It must acquire and release its
	// own connection: Actions calls it before taking one for the
	// statement, which matters for SQLite's single-connection pool.
	// Introspect is nil for actions built from a snapshot.
	Introspect func(ctx context.Context) (*dbschema.Schema, error)
	// Strict makes Plan reject tables that don't exist, rather than
	// skipping them. Snapshots are strict: they're the database's
	// shape as committed, so a missing table is most likely a typo.
	Strict bool

	mu     sync.Mutex
	schema *dbschema.Schema
}

// FromSnapshot builds plan-only Actions from a "database" snapshot,
// which must be of the given dialect ("postgres", ...).
func FromSnapshot(prefix string, d Dialect, dialect string, snap *snapshot.Snapshot) (*Actions, error) {
	if snap.Kind != dbschema.SnapshotKind {
		return nil, fmt.Errorf("snapshot is of kind %q, not %q", snap.Kind, dbschema.SnapshotKind)
	}
	var s dbschema.Schema
	if err := json.Unmarshal(snap.Body, &s); err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	if s.Dialect != dialect {
		return nil, fmt.Errorf("snapshot %s is of a %s database, not %s", snap.Target, s.Dialect, dialect)
	}
	return &Actions{Prefix: prefix, Dialect: d, Strict: true, schema: &s}, nil
}

// PlanAction implements provider.ActionPlanner for a provider whose
// typed actions are a (nil before Configure or a snapshot). Actions
// that aren't typed have nothing to check.
func PlanAction(ctx context.Context, a *Actions, prefix, action string, attrs map[string]cty.Value) (cty.Type, error) {
	if _, ok := Verb(prefix, action); !ok {
		return cty.DynamicPseudoType, nil
	}
	if a == nil {
		return cty.DynamicPseudoType, provider.ErrNoSchema
	}
	return a.Plan(ctx, action, attrs)
}

// Verb returns "insert", "update" or "delete" for one of this
// provider's typed actions, or false.
func (a *Actions) Verb(action string) (string, bool) { return Verb(a.Prefix, action) }

// Verb returns "insert", "update" or "delete" when action is one of
// the typed actions for prefix, or false.
func Verb(prefix, action string) (string, bool) {
	verb, ok := strings.CutPrefix(action, prefix+"_")
	if !ok {
		return "", false
	}
	switch verb {
	case "insert", "update", "delete":
		return verb, true
	}
	return "", false
}

// Plan checks a typed action before the run and returns its output
// type: for an insert, an object of the table's columns. Against the
// live database a table that doesn't exist yet is skipped, because an
// earlier action in the scenario may create it (Execute checks again);
// against a snapshot (Strict) it's an error.
func (a *Actions) Plan(ctx context.Context, action string, attrs map[string]cty.Value) (cty.Type, error) {
	verb, _ := a.Verb(action)
	table := attrs["table"]
	if !table.IsKnown() || table.IsNull() {
		return cty.DynamicPseudoType, nil
	}
	s, err := a.load(ctx, false)
	if err != nil {
		return cty.DynamicPseudoType, err
	}
	t, err := s.Lookup(table.AsString())
	if errors.Is(err, dbschema.ErrNoTable) && !a.Strict {
		return cty.DynamicPseudoType, nil
	}
	if err != nil {
		if a.Strict {
			return cty.DynamicPseudoType, fmt.Errorf("%w in the schema snapshot; if an earlier action creates it, run the scenario once and re-run \"orchard schema pull\"", err)
		}
		return cty.DynamicPseudoType, err
	}
	if err := check(verb, t, attrs); err != nil {
		return cty.DynamicPseudoType, err
	}
	if verb != "insert" {
		return cty.DynamicPseudoType, nil // the action schema declares update/delete outputs
	}
	return a.rowType(t), nil
}

// rowType is the output type of an insert into t: an object of its
// columns, typed by kind (JSON and other kinds stay dynamic). Without
// RETURNING, a table with no primary key returns its values instead,
// so its type isn't known.
func (a *Actions) rowType(t *dbschema.Table) cty.Type {
	if !a.Dialect.Returning && len(t.PrimaryKey) == 0 {
		return cty.DynamicPseudoType
	}
	attrs := make(map[string]cty.Type, len(t.Columns))
	for _, c := range t.Columns {
		attrs[c.Name] = dbschema.KindType(c.Kind)
	}
	return cty.Object(attrs)
}

// Execute validates and runs a typed action, returning its outputs.
func (a *Actions) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	verb, _ := a.Verb(action)
	t, err := a.table(ctx, attrs["table"].AsString())
	if err != nil {
		return nil, err
	}
	if err := check(verb, t, attrs); err != nil {
		return nil, err
	}

	conn, err := a.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	switch verb {
	case "insert":
		return Insert(ctx, conn, a.Dialect, t, attrs["values"])
	case "update":
		return Update(ctx, conn, a.Dialect, t, attrs["set"], attrs["where"])
	default:
		return Delete(ctx, conn, a.Dialect, t, attrs["where"])
	}
}

// Inverse describes how to undo a typed insert: delete the row by its
// primary key, read from the inserted row. Updates, deletes, tables
// without a primary key, and rows whose key didn't come back have no
// automatic undo. It uses the schema cached when the insert ran, so it
// does no I/O.
func (a *Actions) Inverse(action string, attrs, outputs map[string]cty.Value) *provider.Invocation {
	if verb, _ := a.Verb(action); verb != "insert" {
		return nil
	}
	a.mu.Lock()
	s := a.schema
	a.mu.Unlock()
	table := attrs["table"]
	if s == nil || !table.IsKnown() || table.IsNull() {
		return nil
	}
	t, err := s.Lookup(table.AsString())
	if err != nil || len(t.PrimaryKey) == 0 {
		return nil
	}
	where := make(map[string]cty.Value, len(t.PrimaryKey))
	for _, col := range t.PrimaryKey {
		v, ok := outputs[col]
		if !ok || !v.IsKnown() || v.IsNull() {
			return nil
		}
		where[col] = v
	}
	return &provider.Invocation{
		Action: a.Prefix + "_delete",
		Attrs: map[string]cty.Value{
			"table": cty.StringVal(t.Key()),
			"where": cty.ObjectVal(where),
		},
	}
}

// table looks a table up, re-reading the schema once on a miss in case
// an earlier action created it.
func (a *Actions) table(ctx context.Context, name string) (*dbschema.Table, error) {
	s, err := a.load(ctx, false)
	if err != nil {
		return nil, err
	}
	t, err := s.Lookup(name)
	if !errors.Is(err, dbschema.ErrNoTable) {
		return t, err
	}
	if s, err = a.load(ctx, true); err != nil {
		return nil, err
	}
	return s.Lookup(name)
}

// load returns the cached schema, reading it on first use or when
// refresh is set.
func (a *Actions) load(ctx context.Context, refresh bool) (*dbschema.Schema, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.schema != nil && (!refresh || a.Introspect == nil) {
		return a.schema, nil
	}
	s, err := a.Introspect(ctx)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	a.schema = s
	return s, nil
}

func check(verb string, t *dbschema.Table, attrs map[string]cty.Value) error {
	switch verb {
	case "insert":
		return dbschema.CheckInsert(t, attrs["values"])
	case "update":
		return dbschema.CheckUpdate(t, attrs["set"], attrs["where"])
	default:
		return dbschema.CheckDelete(t, attrs["where"])
	}
}

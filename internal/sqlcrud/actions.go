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
	// planned records the tables typed inserts were planned into, in
	// plan order, for foreign-key hints.
	planned map[string]bool
	// owned records the rows this run's typed inserts created (RowKey),
	// so a later update of them is restored on teardown by default.
	owned map[string]bool
	// lastUpdate is what the most recent typed update changed, for
	// Inverse to describe how to restore it. The engine runs actions one
	// at a time and calls Inverse straight after Execute.
	// ponytail: a single slot; key it by action if actions ever run in
	// parallel.
	lastUpdate *updated
}

// updated is what a typed update changed: the previous values of the
// set columns, by primary key, for the rows it matched.
type updated struct {
	table     *dbschema.Table
	rows      []map[string]cty.Value
	truncated bool // more than restoreLimit rows matched
	owned     bool // every matched row was inserted earlier in this run
}

// restoreLimit caps how many rows a typed update records previous
// values for; an update matching more gets no automatic restore, so a
// broad update doesn't fill the run record.
const restoreLimit = 1000

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
func PlanAction(ctx context.Context, a *Actions, prefix, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	if _, ok := Verb(prefix, action); !ok {
		return provider.Plan{}, nil
	}
	if a == nil {
		return provider.Plan{}, provider.ErrNoSchema
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
	case "insert", "update", "delete", "restore":
		// restore is internal: the undo a typed update records. It's not
		// in the provider's schema, so scenarios can't call it.
		return verb, true
	}
	return "", false
}

// Plan checks a typed action before the run and returns its output
// type (for an insert, an object of the table's columns) and any hints.
// Against the live database a table that doesn't exist yet is skipped,
// because an earlier action in the scenario may create it (Execute
// checks again); against a snapshot (Strict) it's an error.
func (a *Actions) Plan(ctx context.Context, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	verb, _ := a.Verb(action)
	if verb == "restore" {
		return provider.Plan{}, nil
	}
	table := attrs["table"]
	if !table.IsKnown() || table.IsNull() {
		return provider.Plan{}, nil
	}
	s, err := a.load(ctx, false)
	if err != nil {
		return provider.Plan{}, err
	}
	t, err := s.Lookup(table.AsString())
	if errors.Is(err, dbschema.ErrNoTable) && !a.Strict {
		return provider.Plan{}, nil
	}
	if err != nil {
		if a.Strict {
			return provider.Plan{}, fmt.Errorf("%w in the schema snapshot; if an earlier action creates it, run the scenario once and re-run \"orchard schema pull\"", err)
		}
		return provider.Plan{}, err
	}
	if err := check(verb, t, attrs); err != nil {
		return provider.Plan{}, err
	}

	var plan provider.Plan
	switch verb {
	case "insert":
		plan.Hints = a.foreignKeyHints(t, attrs["values"])
		plan.Outputs = a.rowType(t)
		a.mu.Lock()
		if a.planned == nil {
			a.planned = map[string]bool{}
		}
		a.planned[t.Key()] = true
		a.mu.Unlock()
	case "update":
		plan.Hints = a.foreignKeyHints(t, attrs["set"])
	}
	return plan, nil
}

// foreignKeyHints flags single-column foreign keys set to a fixed
// value when the referenced table is inserted into earlier in the
// scenario: the author most likely meant to reference that insert's
// output, and a fixed id would point at whatever row happens to have it.
// ponytail: single-column keys only; composite foreign keys are rare in
// seed data.
func (a *Actions) foreignKeyHints(t *dbschema.Table, values cty.Value) []string {
	ty := values.Type()
	if !values.IsKnown() || values.IsNull() || (!ty.IsObjectType() && !ty.IsMapType()) {
		return nil
	}
	fields := values.AsValueMap()
	a.mu.Lock()
	defer a.mu.Unlock()
	var hints []string
	for _, fk := range t.ForeignKeys {
		if len(fk.Columns) != 1 || len(fk.ReferencedColumns) != 1 || !a.planned[fk.References] {
			continue
		}
		v, ok := fields[fk.Columns[0]]
		if !ok || !v.IsWhollyKnown() || v.IsNull() {
			continue
		}
		hints = append(hints, fmt.Sprintf(
			"%s is set to a fixed value (%s), but it references %s.%s, which this scenario inserts into earlier; did you mean that insert's output, e.g. action.%s_insert.<name>.%s?",
			fk.Columns[0], displayValue(v), fk.References, fk.ReferencedColumns[0], a.Prefix, fk.ReferencedColumns[0]))
	}
	return hints
}

func displayValue(v cty.Value) string {
	switch v.Type() {
	case cty.String:
		return fmt.Sprintf("%q", v.AsString())
	case cty.Number:
		return v.AsBigFloat().Text('f', -1)
	default:
		return v.Type().FriendlyName()
	}
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
	if verb != "restore" {
		if err := check(verb, t, attrs); err != nil {
			return nil, err
		}
	}

	conn, err := a.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	switch verb {
	case "insert":
		row, err := Insert(ctx, conn, a.Dialect, t, attrs["values"])
		if err == nil {
			a.own(t, row)
		}
		return row, err
	case "update":
		return a.update(ctx, conn, t, attrs["set"], attrs["where"])
	case "restore":
		return inTx(ctx, conn, func(tx *sql.Tx) (map[string]cty.Value, error) {
			return Restore(ctx, tx, a.Dialect, t, attrs["rows"])
		})
	default:
		return Delete(ctx, conn, a.Dialect, t, attrs["where"])
	}
}

// update runs a typed update, first reading the previous values of the
// columns it sets, in the same transaction, so Inverse can describe how
// to restore them.
func (a *Actions) update(ctx context.Context, conn *sql.Conn, t *dbschema.Table, set, where cty.Value) (map[string]cty.Value, error) {
	a.setLastUpdate(nil)
	if len(t.PrimaryKey) == 0 {
		return Update(ctx, conn, a.Dialect, t, set, where)
	}
	cols := make([]string, 0, len(set.AsValueMap()))
	for _, f := range sortedFields(set) {
		cols = append(cols, f.name)
	}
	var before []map[string]cty.Value
	out, err := inTx(ctx, conn, func(tx *sql.Tx) (map[string]cty.Value, error) {
		var err error
		if before, err = RowsMatching(ctx, tx, a.Dialect, t, cols, where, restoreLimit); err != nil {
			return nil, err
		}
		return Update(ctx, tx, a.Dialect, t, set, where)
	})
	if err != nil {
		return nil, err
	}
	u := &updated{table: t, truncated: len(before) > restoreLimit, owned: true}
	if !u.truncated {
		u.rows = before
	}
	a.mu.Lock()
	for _, row := range before {
		if key, ok := RowKey(t, row); !ok || !a.owned[key] {
			u.owned = false
		}
	}
	a.mu.Unlock()
	a.setLastUpdate(u)
	return out, nil
}

func (a *Actions) setLastUpdate(u *updated) {
	a.mu.Lock()
	a.lastUpdate = u
	a.mu.Unlock()
}

// own records a row this run inserted.
func (a *Actions) own(t *dbschema.Table, row map[string]cty.Value) {
	key, ok := RowKey(t, row)
	if !ok {
		return
	}
	a.mu.Lock()
	if a.owned == nil {
		a.owned = map[string]bool{}
	}
	a.owned[key] = true
	a.mu.Unlock()
}

// inTx runs fn in a transaction on conn, committing if it succeeds.
func inTx(ctx context.Context, conn *sql.Conn, fn func(*sql.Tx) (map[string]cty.Value, error)) (map[string]cty.Value, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	out, err := fn(tx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return out, tx.Commit()
}

// Inverse describes how to undo a typed action. An insert is undone by
// deleting the row by its primary key, read from the inserted row; an
// update by restoring the values it overwrote (see restoreInvocation).
// Deletes, tables without a primary key, and rows whose key didn't come
// back have no automatic undo. It uses state captured when the action
// ran, so it does no I/O.
func (a *Actions) Inverse(action string, attrs, outputs map[string]cty.Value, opts provider.InverseOptions) *provider.Invocation {
	verb, _ := a.Verb(action)
	if verb == "update" {
		return a.restoreInvocation(opts)
	}
	if verb != "insert" {
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

// restoreInvocation describes how to undo the update that just ran:
// write back the previous values of the columns it set. Rows this run
// inserted are restored by default; others only when the scenario asks
// (managed_teardown = true), since rewriting rows Orchard didn't create
// could undo someone else's changes. Updates that matched no rows, or
// more than restoreLimit, record nothing.
func (a *Actions) restoreInvocation(opts provider.InverseOptions) *provider.Invocation {
	a.mu.Lock()
	u := a.lastUpdate
	a.lastUpdate = nil
	a.mu.Unlock()
	if u == nil || u.truncated || len(u.rows) == 0 || (!u.owned && !opts.Requested) {
		return nil
	}
	rows := make([]cty.Value, len(u.rows))
	for i, r := range u.rows {
		rows[i] = cty.ObjectVal(r)
	}
	return &provider.Invocation{
		Action: a.Prefix + "_restore",
		Attrs: map[string]cty.Value{
			"table": cty.StringVal(u.table.Key()),
			"rows":  cty.TupleVal(rows),
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

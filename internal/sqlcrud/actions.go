package sqlcrud

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

	// mu guards schema, planned and owned. It's only held briefly:
	// never while waiting for a connection, or one goroutine holding it
	// could wait on SQLite's single connection while the goroutine
	// holding that connection waits on mu.
	mu     sync.Mutex
	schema *dbschema.Schema
	// planned records the tables typed inserts were planned into, in
	// plan order, for foreign-key hints.
	planned map[string]bool
	// owned records the rows this run's typed inserts created (RowKey),
	// so a later update of them is restored on teardown by default.
	owned map[string]bool
}

// change is what one typed action did, for its undo.
type change struct {
	// inserted is true when an insert created its row, so its undo is a
	// delete. An insert that found its row already there (on_conflict =
	// ignore or update) is undone by a restore, or nothing.
	inserted bool
	// update is what an update, or an upsert that found its row,
	// changed; nil if nothing.
	update *updated
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
			fk.Columns[0], displayValue(v), fk.References, fk.ReferencedColumns[0], a.Prefix, fk.ReferencedColumns[0],
		))
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
	_, out, _, err := a.execute(ctx, action, attrs)
	return out, err
}

// ExecuteUndo runs a typed action like Execute and also describes how
// to undo it (see undo).
func (a *Actions) ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, opts provider.InverseOptions) (map[string]cty.Value, *provider.Invocation, error) {
	t, out, c, err := a.execute(ctx, action, attrs)
	if err != nil {
		return nil, nil, err
	}
	return out, a.undo(t, c, out, opts), nil
}

func (a *Actions) execute(ctx context.Context, action string, attrs map[string]cty.Value) (*dbschema.Table, map[string]cty.Value, change, error) {
	verb, _ := a.Verb(action)
	t, err := a.table(ctx, attrs["table"].AsString())
	if err != nil {
		return nil, nil, change{}, err
	}
	if verb != "restore" {
		if err := check(verb, t, attrs); err != nil {
			return nil, nil, change{}, err
		}
	}

	conn, err := a.Conn(ctx)
	if err != nil {
		return nil, nil, change{}, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var (
		out map[string]cty.Value
		c   change
	)
	switch verb {
	case "insert":
		out, c, err = a.insert(ctx, conn, t, attrs)
	case "update":
		out, c.update, err = a.update(ctx, conn, t, attrs["set"], attrs["where"])
	case "restore":
		out, err = inTx(ctx, conn, func(tx *sql.Tx) (map[string]cty.Value, error) {
			return Restore(ctx, tx, a.Dialect, t, attrs["rows"])
		})
	default:
		out, err = Delete(ctx, conn, a.Dialect, t, attrs["where"])
	}
	if err != nil {
		return nil, nil, change{}, err
	}
	return t, out, c, nil
}

// insert runs a typed insert. With on_conflict = ignore or update it
// first looks for an existing row by the conflict columns, in the same
// transaction: if there is one, ignore returns it unchanged and update
// updates it (recording the previous values, as an update does);
// otherwise it's a plain insert.
// ponytail: look-then-write in a transaction rather than each
// database's ON CONFLICT syntax: one code path for all three, fine for
// a single client seeding data, but not atomic against concurrent
// writers.
func (a *Actions) insert(ctx context.Context, conn *sql.Conn, t *dbschema.Table, attrs map[string]cty.Value) (map[string]cty.Value, change, error) {
	values := attrs["values"]
	mode := conflictMode(attrs)
	if mode == dbschema.ConflictError {
		row, err := Insert(ctx, conn, a.Dialect, t, values)
		if err != nil {
			return nil, change{}, err
		}
		a.own(t, row)
		return row, change{inserted: true}, nil
	}

	key, err := dbschema.ConflictKey(t, conflictColumns(attrs))
	if err != nil {
		return nil, change{}, err
	}
	fields := values.AsValueMap()
	where := map[string]cty.Value{}
	set := map[string]cty.Value{}
	for name, v := range fields {
		if slices.Contains(key, name) {
			where[name] = v
		} else {
			set[name] = v
		}
	}
	setCols := make([]string, 0, len(set))
	for name := range set {
		setCols = append(setCols, name)
	}
	slices.Sort(setCols)

	var (
		existed bool
		before  []map[string]cty.Value
	)
	row, err := inTx(ctx, conn, func(tx *sql.Tx) (map[string]cty.Value, error) {
		var err error
		if before, err = RowsMatching(ctx, tx, a.Dialect, t, setCols, cty.ObjectVal(where), 1); err != nil {
			return nil, err
		}
		if len(before) == 0 {
			return Insert(ctx, tx, a.Dialect, t, values)
		}
		existed = true
		if mode == dbschema.ConflictUpdate && len(set) > 0 {
			if _, err := Update(ctx, tx, a.Dialect, t, cty.ObjectVal(set), cty.ObjectVal(where)); err != nil {
				return nil, err
			}
		}
		return SelectRow(ctx, tx, a.Dialect, t, cty.ObjectVal(where))
	})
	if err != nil {
		return nil, change{}, err
	}
	if !existed {
		a.own(t, row)
		return row, change{inserted: true}, nil
	}
	var c change
	if mode == dbschema.ConflictUpdate && len(set) > 0 && len(t.PrimaryKey) > 0 {
		c.update = &updated{table: t, rows: before, owned: a.ownsAll(t, before)}
	}
	return row, c, nil
}

// conflictMode is the insert's on_conflict, defaulting to error.
func conflictMode(attrs map[string]cty.Value) string {
	if v, ok := attrs["on_conflict"]; ok && !v.IsNull() && v.IsKnown() {
		return v.AsString()
	}
	return dbschema.ConflictError
}

// conflictColumns is the insert's conflict_columns, or nil.
func conflictColumns(attrs map[string]cty.Value) []string {
	v, ok := attrs["conflict_columns"]
	if !ok || v.IsNull() || !v.IsWhollyKnown() {
		return nil
	}
	var cols []string
	for it := v.ElementIterator(); it.Next(); {
		_, c := it.Element()
		cols = append(cols, c.AsString())
	}
	return cols
}

// update runs a typed update, first reading the previous values of the
// columns it sets, in the same transaction, and returns them so its
// undo can restore them (nil for a table without a primary key).
func (a *Actions) update(ctx context.Context, conn *sql.Conn, t *dbschema.Table, set, where cty.Value) (map[string]cty.Value, *updated, error) {
	if len(t.PrimaryKey) == 0 {
		out, err := Update(ctx, conn, a.Dialect, t, set, where)
		return out, nil, err
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
		return nil, nil, err
	}
	u := &updated{table: t, truncated: len(before) > restoreLimit, owned: a.ownsAll(t, before)}
	if !u.truncated {
		u.rows = before
	}
	return out, u, nil
}

// ownsAll reports whether every row was inserted earlier in this run.
func (a *Actions) ownsAll(t *dbschema.Table, rows []map[string]cty.Value) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, row := range rows {
		if key, ok := RowKey(t, row); !ok || !a.owned[key] {
			return false
		}
	}
	return true
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

// undo describes how to undo a typed action on t, from what it did: an
// insert that created its row is undone by deleting the row by its
// primary key, read from the inserted row; an update, or an upsert that
// found its row, by restoring the values it overwrote (see
// restoreInvocation). Deletes, tables without a primary key, and rows
// whose key didn't come back have no automatic undo.
func (a *Actions) undo(t *dbschema.Table, c change, outputs map[string]cty.Value, opts provider.InverseOptions) *provider.Invocation {
	if !c.inserted {
		return a.restoreInvocation(c.update, opts)
	}
	if len(t.PrimaryKey) == 0 {
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

// restoreInvocation describes how to undo update u: write back the
// previous values of the columns it set. Rows this run inserted are
// restored by default; others only when the scenario asks
// (managed_teardown = true), since rewriting rows Orchard didn't create
// could undo someone else's changes. Updates that matched no rows, or
// more than restoreLimit, record nothing.
func (a *Actions) restoreInvocation(u *updated, opts provider.InverseOptions) *provider.Invocation {
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
// refresh is set. It doesn't hold mu while Introspect waits for a
// connection (see mu).
// ponytail: concurrent misses each introspect, last write wins; add a
// singleflight if schemas get big enough for that to matter.
func (a *Actions) load(ctx context.Context, refresh bool) (*dbschema.Schema, error) {
	a.mu.Lock()
	s := a.schema
	a.mu.Unlock()
	if s != nil && (!refresh || a.Introspect == nil) {
		return s, nil
	}
	s, err := a.Introspect(ctx)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	a.mu.Lock()
	a.schema = s
	a.mu.Unlock()
	return s, nil
}

func check(verb string, t *dbschema.Table, attrs map[string]cty.Value) error {
	switch verb {
	case "insert":
		if err := dbschema.CheckInsert(t, attrs["values"]); err != nil {
			return err
		}
		mode := ""
		if v, ok := attrs["on_conflict"]; ok && !v.IsNull() {
			if !v.IsKnown() {
				return nil
			}
			mode = v.AsString()
		}
		return dbschema.CheckConflict(t, mode, conflictColumns(attrs), attrs["values"])
	case "update":
		return dbschema.CheckUpdate(t, attrs["set"], attrs["where"])
	default:
		return dbschema.CheckDelete(t, attrs["where"])
	}
}

package sqlcrud

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/dbschema"
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
	Introspect func(ctx context.Context) (*dbschema.Schema, error)

	mu     sync.Mutex
	schema *dbschema.Schema
}

// Verb returns "insert", "update" or "delete" for one of this
// provider's typed actions, or false.
func (a *Actions) Verb(action string) (string, bool) {
	verb, ok := strings.CutPrefix(action, a.Prefix+"_")
	if !ok {
		return "", false
	}
	switch verb {
	case "insert", "update", "delete":
		return verb, true
	}
	return "", false
}

// Plan checks a typed action before the run. A table that doesn't
// exist yet is skipped rather than rejected, because an earlier action
// in the scenario may create it; Execute checks again.
// ponytail: output types stay dynamic until plan-time typing (spec
// change 3) builds them from the table's columns.
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
	if errors.Is(err, dbschema.ErrNoTable) {
		return cty.DynamicPseudoType, nil
	}
	if err != nil {
		return cty.DynamicPseudoType, err
	}
	return cty.DynamicPseudoType, check(verb, t, attrs)
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
	if a.schema != nil && !refresh {
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

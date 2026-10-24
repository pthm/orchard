// Package sqlite is the builtin Orchard provider for SQLite.
//
// It implements one action, "sqlite_query": run a SQL statement and
// capture the first returned row as outputs, or, for statements that
// return no rows, the last insert id and affected-row count. It uses
// modernc.org/sqlite, a pure-Go driver, so release builds stay
// CGO-free.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/pthm/orchard/internal/sqlcrud"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/snapshot"
)

//go:embed schema.hcl
var schemaHCL []byte

// Provider is the SQLite provider implementation.
//
// A zero Provider is valid but not yet usable — Configure must be
// called before Execute. Schema works on a zero Provider so the
// engine can validate scenarios before a database is opened.
type Provider struct {
	schema protocol.ProviderSchema
	db     *sql.DB
	path   string // as configured; its base name names the database in snapshots
	typed  *sqlcrud.Actions
}

// New returns an unconfigured SQLite provider. Schema is parsed once
// up front so an error in the embedded schema.hcl fails at binary
// startup rather than at first use.
func New() *Provider {
	return &Provider{schema: protocol.MustParseHCL(schemaHCL)}
}

// Compile-time assertion that Provider satisfies the interface.
var _ provider.Provider = (*Provider)(nil)

// Schema returns the embedded provider schema.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

// Configure opens the database at the path config attribute. Like
// sql.Open everywhere, this does not touch the file; Execute surfaces
// open errors on first use.
//
// The pool is capped at one connection. That keeps `:memory:`
// databases (which are per-connection) shared across every action,
// and makes last_insert_rowid()/changes() refer to the statement the
// action just ran.
// ponytail: one connection serialises actions; revisit if parallel
// execution lands (Milestone 6), keeping :memory: on one conn.
func (p *Provider) Configure(ctx context.Context, config map[string]cty.Value) error {
	path, ok := config["path"]
	if !ok || path.IsNull() {
		return fmt.Errorf("sqlite: path is required")
	}
	if path.Type() != cty.String {
		return fmt.Errorf("sqlite: path must be a string, got %s", path.Type().FriendlyName())
	}
	if path.AsString() == "" {
		return fmt.Errorf("sqlite: path must not be empty")
	}
	db, err := sql.Open("sqlite", dsn(path.AsString()))
	if err != nil {
		return fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	p.db = db
	p.path = path.AsString()
	p.typed = &sqlcrud.Actions{
		Prefix:     "sqlite",
		Dialect:    sqlcrud.SQLite,
		Conn:       db.Conn,
		Introspect: p.readSchema,
	}
	return nil
}

// dsn builds a modernc.org/sqlite URI for path with foreign keys
// enforced (SQLite leaves them off by default) and a busy timeout so
// another process holding the file briefly doesn't fail the run.
// '%', '?' and '#' are escaped so they stay part of the file name.
func dsn(path string) string {
	path = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}

// Execute dispatches to the handler for the given action.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if p.db == nil {
		return nil, fmt.Errorf("sqlite: not configured")
	}
	if _, ok := p.typed.Verb(action); ok {
		out, err := p.typed.Execute(ctx, action, attrs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", action, err)
		}
		return out, nil
	}
	switch action {
	case "sqlite_query":
		return p.executeQuery(ctx, attrs)
	default:
		return nil, fmt.Errorf("sqlite: unknown action %q", action)
	}
}

// Compile-time assertion that typed actions are checked before a run.
var _ provider.ActionPlanner = (*Provider)(nil)

// PlanAction checks a typed action before the run: against the live
// schema after Configure, or the snapshot during `orchard plan`.
func (p *Provider) PlanAction(ctx context.Context, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	return sqlcrud.PlanAction(ctx, p.typed, "sqlite", action, attrs)
}

// Compile-time assertion that typed inserts tear themselves down.
var _ provider.Undoer = (*Provider)(nil)

// ExecuteUndo runs an action like Execute and, for a typed action, also
// describes its automatic teardown: deleting an inserted row by its
// primary key, or restoring the previous values of rows an update
// changed.
func (p *Provider) ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, opts provider.InverseOptions) (map[string]cty.Value, *provider.Invocation, error) {
	if p.db == nil {
		return nil, nil, fmt.Errorf("sqlite: not configured")
	}
	if _, ok := p.typed.Verb(action); !ok {
		out, err := p.Execute(ctx, action, attrs)
		return out, nil, err
	}
	out, undo, err := p.typed.ExecuteUndo(ctx, action, attrs, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", action, err)
	}
	return out, undo, nil
}

// Compile-time assertion that `orchard plan` can check typed actions.
var _ provider.PlanConfigurer = (*Provider)(nil)

// PlanConfigure loads the schema snapshot `orchard plan` checks typed
// actions against. Without one, typed actions go unchecked at plan
// time. It doesn't connect.
func (p *Provider) PlanConfigure(_ context.Context, _ map[string]cty.Value, snap *snapshot.Snapshot) error {
	if snap == nil {
		return nil
	}
	typed, err := sqlcrud.FromSnapshot("sqlite", sqlcrud.SQLite, "sqlite", snap)
	if err != nil {
		return err
	}
	p.typed = typed
	return nil
}

// Close closes the database. Safe to call on an unconfigured
// provider or to call multiple times.
func (p *Provider) Close() error {
	if p.db == nil {
		return nil
	}
	err := p.db.Close()
	p.db = nil
	return err
}

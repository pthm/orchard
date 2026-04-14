// Package postgres is the builtin Orchard provider for PostgreSQL.
//
// It implements one action today — "sql", a raw-query runner that
// captures the first returned row as outputs — and is the provider
// the rest of the codebase exercises end-to-end. Schema-aware
// actions (db.insert, db.update, db.delete) land in a later milestone.
package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

//go:embed schema.hcl
var schemaHCL []byte

// Provider is the PostgreSQL provider implementation.
//
// A zero Provider is valid but not yet usable — Configure must be
// called before Execute. Schema works on a zero Provider so the
// engine can validate scenarios against its action set before a
// database connection has been established.
type Provider struct {
	schema protocol.ProviderSchema
	pool   *pgxpool.Pool
}

// New returns an unconfigured PostgreSQL provider. Schema is parsed
// once up front so that any error in the embedded schema.hcl fails
// loudly at binary startup rather than at first use.
func New() *Provider {
	return &Provider{schema: protocol.MustParseHCL(schemaHCL)}
}

// Compile-time assertion that Provider satisfies the interface.
var _ provider.Provider = (*Provider)(nil)

// Schema returns the embedded provider schema.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

// Configure opens a connection pool using the dsn config attribute.
// The pool is lazily tested — pgxpool.New does not dial until first
// use — so Configure will succeed for syntactically-valid DSNs even
// if the server is unreachable. Execute surfaces the connection
// failure later, which is the right place for it.
func (p *Provider) Configure(ctx context.Context, config map[string]cty.Value) error {
	dsn, ok := config["dsn"]
	if !ok || dsn.IsNull() {
		return fmt.Errorf("postgres: dsn is required")
	}
	if dsn.Type() != cty.String {
		return fmt.Errorf("postgres: dsn must be a string, got %s", dsn.Type().FriendlyName())
	}
	pool, err := pgxpool.New(ctx, dsn.AsString())
	if err != nil {
		return fmt.Errorf("postgres: create pool: %w", err)
	}
	p.pool = pool
	return nil
}

// Execute dispatches to the handler for the given action.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("postgres: not configured")
	}
	switch action {
	case "sql":
		return p.executeSQL(ctx, attrs)
	default:
		return nil, fmt.Errorf("postgres: unknown action %q", action)
	}
}

// Close releases the underlying pgxpool. Safe to call on an
// unconfigured provider or to call multiple times.
func (p *Provider) Close() error {
	if p.pool == nil {
		return nil
	}
	p.pool.Close()
	p.pool = nil
	return nil
}

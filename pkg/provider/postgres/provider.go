// Package postgres is the builtin Orchard provider for PostgreSQL.
//
// It offers raw SQL (postgres_query, capturing the first returned row
// as outputs), bulk loading (postgres_copy), and typed, schema-checked
// postgres_insert / _update / _delete, shared with the other SQL
// providers through internal/sqlcrud.
package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/sqlcrud"
	"github.com/pthm/orchard/pkg/dbschema"
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
	// db is a database/sql view of pool, for the typed actions shared
	// with the other SQL providers.
	db    *sql.DB
	typed *sqlcrud.Actions
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
	p.db = stdlib.OpenDBFromPool(pool)
	p.typed = &sqlcrud.Actions{
		Prefix:  "postgres",
		Dialect: sqlcrud.Postgres,
		Conn:    p.db.Conn,
		Introspect: func(ctx context.Context) (*dbschema.Schema, error) {
			return p.introspect(ctx, nil)
		},
	}
	return nil
}

// Execute dispatches to the handler for the given action.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("postgres: not configured")
	}
	if _, ok := p.typed.Verb(action); ok {
		out, err := p.typed.Execute(ctx, action, attrs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", action, err)
		}
		return out, nil
	}
	switch action {
	case "postgres_query":
		return p.executeSQL(ctx, attrs)
	case "postgres_copy":
		return p.executeCopy(ctx, attrs)
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
	_ = p.db.Close()
	p.pool.Close()
	p.pool, p.db = nil, nil
	return nil
}

// Compile-time assertion that typed actions are checked before a run.
var _ provider.ActionPlanner = (*Provider)(nil)

// PlanAction checks a typed action against the live schema before the
// run. Before Configure there's no database to check against.
func (p *Provider) PlanAction(ctx context.Context, action string, attrs map[string]cty.Value) (cty.Type, error) {
	if p.typed == nil {
		return cty.DynamicPseudoType, nil
	}
	if _, ok := p.typed.Verb(action); !ok {
		return cty.DynamicPseudoType, nil
	}
	return p.typed.Plan(ctx, action, attrs)
}

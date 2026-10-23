// Package mysql is the builtin Orchard provider for MySQL (and
// MariaDB).
//
// It implements one action, "mysql_query": run a SQL statement and
// capture the first returned row as outputs, or, for statements that
// return no rows, LAST_INSERT_ID() and ROW_COUNT(). MySQL has no
// RETURNING clause, so last_insert_id is how an INSERT's new id gets
// wired into the next step.
package mysql

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/internal/sqlcrud"
	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/snapshot"
)

//go:embed schema.hcl
var schemaHCL []byte

// Provider is the MySQL provider implementation.
//
// A zero Provider is valid but not yet usable — Configure must be
// called before Execute. Schema works on a zero Provider so the
// engine can validate scenarios before a connection is opened.
type Provider struct {
	schema protocol.ProviderSchema
	db     *sql.DB
	typed  *sqlcrud.Actions
}

// New returns an unconfigured MySQL provider. Schema is parsed once
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

// Configure parses the dsn config attribute and opens a pool. Like
// sql.Open everywhere, this does not dial; Execute surfaces
// connection errors on first use.
func (p *Provider) Configure(ctx context.Context, config map[string]cty.Value) error {
	dsn, ok := config["dsn"]
	if !ok || dsn.IsNull() {
		return fmt.Errorf("mysql: dsn is required")
	}
	if dsn.Type() != cty.String {
		return fmt.Errorf("mysql: dsn must be a string, got %s", dsn.Type().FriendlyName())
	}
	cfg, err := parseDSN(dsn.AsString())
	if err != nil {
		return fmt.Errorf("mysql: %w", err)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return fmt.Errorf("mysql: %w", err)
	}
	p.db = sql.OpenDB(connector)
	p.typed = &sqlcrud.Actions{
		Prefix:     "mysql",
		Dialect:    sqlcrud.MySQL,
		Conn:       p.db.Conn,
		Introspect: p.readSchema,
	}
	return nil
}

// parseDSN accepts either the driver's own DSN format
// (user:pass@tcp(host:3306)/db) or a mysql:// URL, so a DATABASE_URL
// from the environment works unchanged. Two options are always on:
//
//   - parseTime, so DATE/DATETIME/TIMESTAMP columns decode as times
//     (and become RFC 3339 strings) rather than raw bytes.
//   - multiStatements, so one query can carry BEGIN; ...; COMMIT; the
//     same as the postgres and sqlite providers.
func parseDSN(dsn string) (*mysql.Config, error) {
	if strings.HasPrefix(dsn, "mysql://") {
		converted, err := urlToDriverDSN(dsn)
		if err != nil {
			return nil, err
		}
		dsn = converted
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid dsn: %w", err)
	}
	cfg.ParseTime = true
	cfg.MultiStatements = true
	return cfg, nil
}

// urlToDriverDSN rewrites mysql://user:pass@host:port/db?params into
// the driver's user:pass@tcp(host:port)/db?params. Query parameters
// pass through as driver options.
func urlToDriverDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// url.Parse errors quote the whole URL, password included.
		return "", errors.New("invalid dsn: not a valid mysql:// URL")
	}
	if u.Host == "" {
		return "", errors.New("invalid dsn: mysql:// URL has no host")
	}
	var b strings.Builder
	if u.User != nil {
		b.WriteString(u.User.Username())
		if pass, ok := u.User.Password(); ok {
			b.WriteString(":" + pass)
		}
		b.WriteString("@")
	}
	b.WriteString("tcp(" + u.Host + ")/" + strings.TrimPrefix(u.Path, "/"))
	if u.RawQuery != "" {
		b.WriteString("?" + u.RawQuery)
	}
	return b.String(), nil
}

// Execute dispatches to the handler for the given action.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if p.db == nil {
		return nil, fmt.Errorf("mysql: not configured")
	}
	if _, ok := p.typed.Verb(action); ok {
		out, err := p.typed.Execute(ctx, action, attrs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", action, err)
		}
		return out, nil
	}
	switch action {
	case "mysql_query":
		return p.executeQuery(ctx, attrs)
	default:
		return nil, fmt.Errorf("mysql: unknown action %q", action)
	}
}

// Compile-time assertion that typed actions are checked before a run.
var _ provider.ActionPlanner = (*Provider)(nil)

// PlanAction checks a typed action before the run: against the live
// schema after Configure, or the snapshot during `orchard plan`.
func (p *Provider) PlanAction(ctx context.Context, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	return sqlcrud.PlanAction(ctx, p.typed, "mysql", action, attrs)
}

// Compile-time assertion that typed inserts tear themselves down.
var _ provider.Inverter = (*Provider)(nil)

// Inverse describes the automatic teardown of a typed insert: deleting
// the row by its primary key.
func (p *Provider) Inverse(action string, attrs, outputs map[string]cty.Value) *provider.Invocation {
	if p.typed == nil {
		return nil
	}
	return p.typed.Inverse(action, attrs, outputs)
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
	typed, err := sqlcrud.FromSnapshot("mysql", sqlcrud.MySQL, "mysql", snap)
	if err != nil {
		return err
	}
	p.typed = typed
	return nil
}

// Close closes the pool. Safe to call on an unconfigured provider or
// to call multiple times.
func (p *Provider) Close() error {
	if p.db == nil {
		return nil
	}
	err := p.db.Close()
	p.db = nil
	return err
}

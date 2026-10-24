// Package redis is the builtin Orchard provider for Redis and
// wire-compatible stores (Valkey, Dragonfly, KeyDB).
//
// It offers redis_command, a raw command runner, and typed redis_set and
// redis_hset actions for the two shapes most seed data takes: a value
// under a key, and a hash of fields.
package redis

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"sync"

	goredis "github.com/redis/go-redis/v9"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

//go:embed schema.hcl
var schemaHCL []byte

// Provider is the Redis provider implementation.
//
// A zero Provider is valid but not yet usable — Configure must be
// called before Execute. Schema works on a zero Provider so the engine
// can validate scenarios before connecting.
type Provider struct {
	schema protocol.ProviderSchema
	client *goredis.Client
	// owned records the keys typed actions created this run, so a later
	// overwrite of them is restored on teardown by default. mu guards it.
	mu    sync.Mutex
	owned map[string]bool
}

// New returns an unconfigured Redis provider.
func New() *Provider {
	return &Provider{schema: protocol.MustParseHCL(schemaHCL)}
}

var (
	_ provider.Provider      = (*Provider)(nil)
	_ provider.ActionPlanner = (*Provider)(nil)
	_ provider.Undoer        = (*Provider)(nil)
)

// Schema returns the embedded provider schema.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

// Configure connects to the url config attribute and sends PING, so a
// bad URL or an unreachable server fails before any action runs.
func (p *Provider) Configure(ctx context.Context, config map[string]cty.Value) error {
	raw, ok := config["url"]
	if !ok || raw.IsNull() {
		return fmt.Errorf("redis: url is required")
	}
	if raw.Type() != cty.String {
		return fmt.Errorf("redis: url must be a string, got %s", raw.Type().FriendlyName())
	}
	// url.Parse errors quote the whole URL, password included.
	if _, err := url.Parse(raw.AsString()); err != nil {
		return errors.New("redis: url isn't a valid redis:// or rediss:// URL")
	}
	opts, err := goredis.ParseURL(raw.AsString())
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	client := goredis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return fmt.Errorf("redis: connect to %s: %w", opts.Addr, err)
	}
	p.client = client
	return nil
}

// Execute dispatches to the handler for the given action.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	out, _, err := p.execute(ctx, action, attrs)
	return out, err
}

// ExecuteUndo runs an action like Execute and also describes its undo
// (see written.undo).
func (p *Provider) ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, opts provider.InverseOptions) (map[string]cty.Value, *provider.Invocation, error) {
	out, w, err := p.execute(ctx, action, attrs)
	if err != nil {
		return nil, nil, err
	}
	return out, w.undo(opts), nil
}

// execute runs an action, returning what a typed action wrote (nil for
// the others).
func (p *Provider) execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, *written, error) {
	if p.client == nil {
		return nil, nil, fmt.Errorf("redis: not configured")
	}
	if err := check(action, attrs); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", action, err)
	}
	var (
		out map[string]cty.Value
		w   *written
		err error
	)
	switch action {
	case "redis_command":
		out, err = p.command(ctx, attrs)
	case "redis_set":
		out, w, err = p.set(ctx, attrs)
	case "redis_hset":
		out, w, err = p.hset(ctx, attrs)
	case "redis_restore":
		// Internal: the undo ExecuteUndo records for an overwrite. It
		// isn't in the schema, so scenarios can't call it.
		out, err = p.restore(ctx, attrs)
	default:
		return nil, nil, fmt.Errorf("redis: unknown action %q", action)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", action, err)
	}
	return out, w, nil
}

// PlanAction runs the checks that need no server, at plan and before a
// run: args, ttl, null values and on_conflict. Output types are static
// in the schema.
func (p *Provider) PlanAction(_ context.Context, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	return provider.Plan{}, check(action, attrs)
}

// Close closes the client. Safe to call on an unconfigured provider or
// more than once.
func (p *Provider) Close() error {
	if p.client == nil {
		return nil
	}
	err := p.client.Close()
	p.client = nil
	return err
}

// check validates an action's attributes without a server. Unknown
// values (references to other actions, at plan time) are skipped.
func check(action string, attrs map[string]cty.Value) error {
	if mode, ok := attrs["on_conflict"]; ok && mode.IsKnown() && !mode.IsNull() {
		if m := mode.AsString(); m != conflictUpdate && m != conflictIgnore {
			return fmt.Errorf("on_conflict must be %q or %q; got %q", conflictUpdate, conflictIgnore, m)
		}
	}
	if ttl, ok := attrs["ttl"]; ok {
		if _, _, err := parseTTL(ttl); err != nil {
			return err
		}
	}
	switch action {
	case "redis_command":
		args := attrs["args"]
		if !args.IsWhollyKnown() {
			return nil
		}
		if args.IsNull() || !args.CanIterateElements() || args.Type().IsMapType() || args.Type().IsObjectType() {
			return fmt.Errorf("args must be a list like [\"SET\", \"key\", \"value\"]")
		}
		if args.LengthInt() == 0 {
			return fmt.Errorf("args names no command")
		}
		for it := args.ElementIterator(); it.Next(); {
			_, v := it.Element()
			if _, err := commandArg(v); err != nil {
				return err
			}
		}
	case "redis_set":
		if v := attrs["value"]; v.IsKnown() && v.IsNull() {
			return fmt.Errorf("value is null; delete the key with redis_command instead")
		}
	case "redis_hset":
		fields := attrs["fields"]
		if !fields.IsKnown() {
			return nil
		}
		if fields.IsNull() || (!fields.Type().IsObjectType() && !fields.Type().IsMapType()) {
			return fmt.Errorf("fields must be an object of field = value")
		}
		if fields.LengthInt() == 0 {
			return fmt.Errorf("fields names no fields")
		}
		for name, v := range fields.AsValueMap() {
			if v.IsKnown() && v.IsNull() {
				return fmt.Errorf("field %q is null; remove it with redis_command HDEL instead", name)
			}
		}
	}
	return nil
}

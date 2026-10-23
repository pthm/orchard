package redis

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
)

// on_conflict values, matching typed SQL inserts.
const (
	conflictUpdate = "update"
	conflictIgnore = "ignore"
)

// dumpLimit caps the DUMP a typed action keeps to restore an
// overwritten key, so a big value doesn't fill the run record.
const dumpLimit = 1 << 20

func conflictMode(attrs map[string]cty.Value) string {
	if v, ok := attrs["on_conflict"]; ok && v.IsKnown() && !v.IsNull() {
		return v.AsString()
	}
	return conflictUpdate
}

// written is what the most recent typed action did to its key, for
// Inverse to describe the undo.
type written struct {
	key       string
	created   bool
	wrote     bool     // false when on_conflict = "ignore" left the key alone
	owned     bool     // an earlier typed action this run created the key
	dump      string   // the key before the write; empty if too big to keep
	pttl      int64    // its expiry in ms then, 0 for none
	newFields []string // redis_hset fields that didn't exist
	hash      bool
}

// Inverse describes the undo of the typed action that just ran: DEL a
// key it created; RESTORE one it overwrote, when the key is owned or
// the scenario asks (managed_teardown = true); for an unowned hash,
// HDEL just the fields it added. Otherwise nothing.
func (p *Provider) Inverse(action string, _, _ map[string]cty.Value, opts provider.InverseOptions) *provider.Invocation {
	w := p.last
	p.last = nil
	if w == nil || !w.wrote || (action != "redis_set" && action != "redis_hset") {
		return nil
	}
	if w.created {
		return command("DEL", w.key)
	}
	if (w.owned || opts.Requested) && w.dump != "" {
		return &provider.Invocation{
			Action: "redis_restore",
			Attrs: map[string]cty.Value{
				"key":  cty.StringVal(w.key),
				"dump": cty.StringVal(base64.StdEncoding.EncodeToString([]byte(w.dump))),
				"pttl": cty.NumberIntVal(w.pttl),
			},
		}
	}
	if w.hash && !w.owned && !opts.Requested && len(w.newFields) > 0 {
		return command(append([]string{"HDEL", w.key}, w.newFields...)...)
	}
	return nil
}

func command(args ...string) *provider.Invocation {
	vals := make([]cty.Value, len(args))
	for i, a := range args {
		vals[i] = cty.StringVal(a)
	}
	return &provider.Invocation{Action: "redis_command", Attrs: map[string]cty.Value{"args": cty.TupleVal(vals)}}
}

// remember records what a typed action did, and the key as owned when
// it created it.
func (p *Provider) remember(w *written) {
	if p.owned == nil {
		p.owned = map[string]bool{}
	}
	w.owned = p.owned[w.key]
	if w.created {
		p.owned[w.key] = true
	}
	p.last = w
}

// capture queues DUMP and PTTL of key ahead of a write in the same
// MULTI/EXEC, so the undo can put back exactly what was there.
type capture struct {
	dump *goredis.StringCmd
	pttl *goredis.DurationCmd
}

func captureKey(ctx context.Context, tx goredis.Pipeliner, key string) capture {
	return capture{dump: tx.Dump(ctx, key), pttl: tx.PTTL(ctx, key)}
}

func (c capture) fill(w *written) {
	if d := c.dump.Val(); len(d) <= dumpLimit {
		w.dump = d
	}
	if t := c.pttl.Val(); t > 0 {
		w.pttl = t.Milliseconds()
	}
}

// txErr is the first real error among a transaction's commands. DUMP of
// a missing key replies nil, which go-redis reports as an error.
func txErr(cmds []goredis.Cmder, err error) error {
	if err == nil || !errors.Is(err, goredis.Nil) {
		return err
	}
	for _, c := range cmds {
		if e := c.Err(); e != nil && !errors.Is(e, goredis.Nil) {
			return e
		}
	}
	return nil
}

// command sends args as one command and returns the reply as result.
func (p *Provider) command(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	var args []any
	for it := attrs["args"].ElementIterator(); it.Next(); {
		_, v := it.Element()
		s, err := commandArg(v)
		if err != nil {
			return nil, err
		}
		args = append(args, s)
	}
	reply, err := p.client.Do(ctx, args...).Result()
	if errors.Is(err, goredis.Nil) {
		reply, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]cty.Value{"result": fromReply(reply)}, nil
}

// restore is the internal redis_restore action, the undo of an
// overwrite: RESTORE key pttl dump REPLACE.
func (p *Provider) restore(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	dump, err := base64.StdEncoding.DecodeString(attrs["dump"].AsString())
	if err != nil {
		return nil, fmt.Errorf("dump: %w", err)
	}
	ms, _ := attrs["pttl"].AsBigFloat().Int64()
	key := attrs["key"].AsString()
	if err := p.client.RestoreReplace(ctx, key, time.Duration(ms)*time.Millisecond, string(dump)).Err(); err != nil {
		return nil, err
	}
	return map[string]cty.Value{}, nil
}

// set stores value under key. With on_conflict = "update" it's a plain
// SET, preceded in the same MULTI/EXEC by EXISTS and a capture of the
// old value so created is exact and an overwrite can be undone. With
// "ignore" it's SET NX: an existing key is left alone.
func (p *Provider) set(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	key := attrs["key"].AsString()
	value, err := encodeValue(attrs["value"])
	if err != nil {
		return nil, err
	}
	ttl, _, err := parseTTL(attrs["ttl"])
	if err != nil {
		return nil, err
	}

	w := &written{key: key}
	if conflictMode(attrs) == conflictIgnore {
		if w.created, err = p.client.SetNX(ctx, key, value, ttl).Result(); err != nil {
			return nil, err
		}
		w.wrote = w.created
	} else {
		var (
			exists *goredis.IntCmd
			old    capture
		)
		cmds, err := p.client.TxPipelined(ctx, func(tx goredis.Pipeliner) error {
			exists = tx.Exists(ctx, key)
			old = captureKey(ctx, tx, key)
			tx.Set(ctx, key, value, ttl)
			return nil
		})
		if err := txErr(cmds, err); err != nil {
			return nil, err
		}
		w.created, w.wrote = exists.Val() == 0, true
		old.fill(w)
	}
	p.remember(w)
	return map[string]cty.Value{"key": cty.StringVal(key), "created": cty.BoolVal(w.created)}, nil
}

// hset sets fields on the hash at key, then its expiry when ttl is set.
// With on_conflict = "update" it reads EXISTS and HEXISTS per field,
// captures the old hash, and writes in one MULTI/EXEC, so created and
// new_fields are exact. With "ignore" it writes only if the key doesn't
// exist, under WATCH.
func (p *Provider) hset(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	key := attrs["key"].AsString()
	ttl, hasTTL, err := parseTTL(attrs["ttl"])
	if err != nil {
		return nil, err
	}
	fields := attrs["fields"].AsValueMap()
	names := make([]string, 0, len(fields))
	pairs := make([]any, 0, 2*len(fields))
	for name, v := range fields {
		s, err := encodeValue(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		names = append(names, name)
		pairs = append(pairs, name, s)
	}

	write := func(tx goredis.Pipeliner) {
		tx.HSet(ctx, key, pairs...)
		if hasTTL {
			tx.PExpire(ctx, key, ttl)
		}
	}
	w := &written{key: key, hash: true}
	out := func() map[string]cty.Value {
		p.remember(w)
		return map[string]cty.Value{
			"key":        cty.StringVal(key),
			"created":    cty.BoolVal(w.created),
			"new_fields": stringList(w.newFields),
		}
	}

	if conflictMode(attrs) == conflictIgnore {
		err := p.client.Watch(ctx, func(tx *goredis.Tx) error {
			n, err := tx.Exists(ctx, key).Result()
			if err != nil || n > 0 {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
				write(pipe)
				return nil
			})
			w.created = err == nil
			return err
		}, key)
		if err != nil {
			return nil, err
		}
		if w.created {
			w.wrote, w.newFields = true, names
		}
		return out(), nil
	}

	var (
		exists    *goredis.IntCmd
		hasFields = make([]*goredis.BoolCmd, len(names))
		old       capture
	)
	cmds, err := p.client.TxPipelined(ctx, func(tx goredis.Pipeliner) error {
		exists = tx.Exists(ctx, key)
		for i, name := range names {
			hasFields[i] = tx.HExists(ctx, key, name)
		}
		old = captureKey(ctx, tx, key)
		write(tx)
		return nil
	})
	if err := txErr(cmds, err); err != nil {
		return nil, err
	}
	for i, name := range names {
		if !hasFields[i].Val() {
			w.newFields = append(w.newFields, name)
		}
	}
	w.created, w.wrote = exists.Val() == 0, true
	old.fill(w)
	return out(), nil
}

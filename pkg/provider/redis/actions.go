package redis

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
	"github.com/zclconf/go-cty/cty"
)

// on_conflict values, matching typed SQL inserts.
const (
	conflictUpdate = "update"
	conflictIgnore = "ignore"
)

func conflictMode(attrs map[string]cty.Value) string {
	if v, ok := attrs["on_conflict"]; ok && v.IsKnown() && !v.IsNull() {
		return v.AsString()
	}
	return conflictUpdate
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

// set stores value under key. With on_conflict = "update" it's a plain
// SET, preceded in the same MULTI/EXEC by EXISTS so created is exact.
// With "ignore" it's SET NX: an existing key is left alone.
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

	var created bool
	if conflictMode(attrs) == conflictIgnore {
		created, err = p.client.SetNX(ctx, key, value, ttl).Result()
		if err != nil {
			return nil, err
		}
	} else {
		var exists *goredis.IntCmd
		_, err = p.client.TxPipelined(ctx, func(tx goredis.Pipeliner) error {
			exists = tx.Exists(ctx, key)
			tx.Set(ctx, key, value, ttl)
			return nil
		})
		if err != nil {
			return nil, err
		}
		created = exists.Val() == 0
	}
	return map[string]cty.Value{"key": cty.StringVal(key), "created": cty.BoolVal(created)}, nil
}

// hset sets fields on the hash at key, then its expiry when ttl is set.
// With on_conflict = "update" it reads EXISTS and HEXISTS per field and
// writes in one MULTI/EXEC, so created and new_fields are exact. With
// "ignore" it writes only if the key doesn't exist, under WATCH.
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
	out := func(created bool, newFields []string) map[string]cty.Value {
		return map[string]cty.Value{
			"key":        cty.StringVal(key),
			"created":    cty.BoolVal(created),
			"new_fields": stringList(newFields),
		}
	}

	if conflictMode(attrs) == conflictIgnore {
		created := false
		err := p.client.Watch(ctx, func(tx *goredis.Tx) error {
			n, err := tx.Exists(ctx, key).Result()
			if err != nil || n > 0 {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
				write(pipe)
				return nil
			})
			created = err == nil
			return err
		}, key)
		if err != nil {
			return nil, err
		}
		if !created {
			return out(false, nil), nil
		}
		return out(true, names), nil
	}

	var (
		exists    *goredis.IntCmd
		hasFields = make([]*goredis.BoolCmd, len(names))
	)
	_, err = p.client.TxPipelined(ctx, func(tx goredis.Pipeliner) error {
		exists = tx.Exists(ctx, key)
		for i, name := range names {
			hasFields[i] = tx.HExists(ctx, key, name)
		}
		write(tx)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var newFields []string
	for i, name := range names {
		if !hasFields[i].Val() {
			newFields = append(newFields, name)
		}
	}
	return out(exists.Val() == 0, newFields), nil
}

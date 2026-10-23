package redis

import (
	"context"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
)

// run executes a typed action and returns its undo, then runs the undo
// later via teardown(), the way the engine does.
func runWithUndo(t *testing.T, p *Provider, action string, attrs map[string]cty.Value, requested bool) *provider.Invocation {
	t.Helper()
	out := exec(t, p, action, attrs)
	return p.Inverse(action, attrs, out, provider.InverseOptions{Requested: requested})
}

func teardown(t *testing.T, p *Provider, undos ...*provider.Invocation) {
	t.Helper()
	for i := len(undos) - 1; i >= 0; i-- {
		if undos[i] == nil {
			continue
		}
		if _, err := p.Execute(context.Background(), undos[i].Action, undos[i].Attrs); err != nil {
			t.Fatalf("undo %s: %v", undos[i].Action, err)
		}
	}
}

func setAttrs(key, value string) map[string]cty.Value {
	return map[string]cty.Value{"key": cty.StringVal(key), "value": cty.StringVal(value)}
}

func hsetAttrs(key string, fields map[string]string) map[string]cty.Value {
	f := map[string]cty.Value{}
	for k, v := range fields {
		f[k] = cty.StringVal(v)
	}
	return map[string]cty.Value{"key": cty.StringVal(key), "fields": cty.ObjectVal(f)}
}

func TestTeardownCreatedAndOwned(t *testing.T) {
	p, srv := configured(t)
	a := runWithUndo(t, p, "redis_set", setAttrs("k", "one"), false)
	b := runWithUndo(t, p, "redis_set", setAttrs("k", "two"), false)
	if a == nil || a.Action != "redis_command" || b == nil || b.Action != "redis_restore" {
		t.Fatalf("undos: %#v, %#v", a, b)
	}
	teardown(t, p, a, b)
	if srv.Exists("k") {
		t.Error("created key should be gone")
	}
}

func TestTeardownUnownedOverwrite(t *testing.T) {
	p, srv := configured(t)
	_ = srv.Set("s", "theirs")
	srv.SetTTL("s", time.Minute)
	if u := runWithUndo(t, p, "redis_set", setAttrs("s", "ours"), false); u != nil {
		t.Errorf("unowned, not requested: want no undo, got %#v", u)
	}

	u := runWithUndo(t, p, "redis_set", setAttrs("s", "ours again"), true)
	if u == nil || u.Action != "redis_restore" {
		t.Fatalf("requested: %#v", u)
	}
	teardown(t, p, u)
	if got, _ := srv.Get("s"); got != "ours" {
		t.Errorf("restored value: %q", got)
	}
}

func TestTeardownRestoresExpiry(t *testing.T) {
	p, srv := configured(t)
	_ = srv.Set("e", "theirs")
	srv.SetTTL("e", time.Minute)
	u := runWithUndo(t, p, "redis_set", setAttrs("e", "ours"), true)
	teardown(t, p, u)
	if got, _ := srv.Get("e"); got != "theirs" {
		t.Errorf("value: %q", got)
	}
	if ttl := srv.TTL("e"); ttl <= 0 || ttl > time.Minute {
		t.Errorf("ttl: %v", ttl)
	}
}

func TestTeardownIgnore(t *testing.T) {
	p, srv := configured(t)
	_ = srv.Set("i", "theirs")
	attrs := setAttrs("i", "ours")
	attrs["on_conflict"] = cty.StringVal("ignore")
	if u := runWithUndo(t, p, "redis_set", attrs, true); u != nil {
		t.Errorf("ignored existing key: want no undo, got %#v", u)
	}
	attrs = hsetAttrs("new", map[string]string{"a": "1"})
	attrs["on_conflict"] = cty.StringVal("ignore")
	u := runWithUndo(t, p, "redis_hset", attrs, false)
	teardown(t, p, u)
	if srv.Exists("new") {
		t.Error("created hash should be gone")
	}
}

func TestTeardownSkipsLargeDump(t *testing.T) {
	p, srv := configured(t)
	big := make([]byte, dumpLimit+1)
	_ = srv.Set("big", string(big))
	if u := runWithUndo(t, p, "redis_set", setAttrs("big", "small"), true); u != nil {
		t.Errorf("oversized dump: want no undo, got %s", u.Action)
	}
}

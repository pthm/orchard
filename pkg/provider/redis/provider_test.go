package redis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/zclconf/go-cty/cty"
)

func configured(t *testing.T) (*Provider, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	p := New()
	if err := p.Configure(context.Background(), map[string]cty.Value{
		"url": cty.StringVal("redis://" + srv.Addr() + "/0"),
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, srv
}

func exec(t *testing.T, p *Provider, action string, attrs map[string]cty.Value) map[string]cty.Value {
	t.Helper()
	out, err := p.Execute(context.Background(), action, attrs)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return out
}

func TestSchema(t *testing.T) {
	s := New().Schema()
	for _, a := range []string{"redis_command", "redis_set", "redis_hset"} {
		if _, ok := s.Actions[a]; !ok {
			t.Errorf("missing %s", a)
		}
	}
}

func TestConfigureHidesPassword(t *testing.T) {
	err := New().Configure(context.Background(), map[string]cty.Value{
		"url": cty.StringVal("redis://u:s3cret@host:6379/\x7f"),
	})
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("got %v", err)
	}
}

func TestSet(t *testing.T) {
	p, srv := configured(t)
	out := exec(t, p, "redis_set", map[string]cty.Value{
		"key":   cty.StringVal("session:1"),
		"value": cty.ObjectVal(map[string]cty.Value{"user_id": cty.NumberIntVal(7)}),
		"ttl":   cty.StringVal("1h"),
	})
	if !out["created"].True() {
		t.Error("created: want true")
	}
	if got, _ := srv.Get("session:1"); got != `{"user_id":7}` {
		t.Errorf("value: %q", got)
	}
	if srv.TTL("session:1") != time.Hour {
		t.Errorf("ttl: %v", srv.TTL("session:1"))
	}

	out = exec(t, p, "redis_set", map[string]cty.Value{"key": cty.StringVal("session:1"), "value": cty.NumberFloatVal(9.99)})
	if out["created"].True() {
		t.Error("overwrite: created want false")
	}
	if got, _ := srv.Get("session:1"); got != "9.99" {
		t.Errorf("value: %q", got)
	}

	out = exec(t, p, "redis_set", map[string]cty.Value{
		"key": cty.StringVal("session:1"), "value": cty.True, "on_conflict": cty.StringVal("ignore"),
	})
	if out["created"].True() {
		t.Error("ignore: created want false")
	}
	if got, _ := srv.Get("session:1"); got != "9.99" {
		t.Errorf("ignore wrote: %q", got)
	}
}

// Updating an existing hash is tested against real Redis in
// test/integration: miniredis can't DUMP a hash.
func TestHSet(t *testing.T) {
	p, srv := configured(t)
	out := exec(t, p, "redis_hset", map[string]cty.Value{
		"key":    cty.StringVal("flags:1"),
		"fields": cty.ObjectVal(map[string]cty.Value{"a": cty.True, "b": cty.False}),
		"ttl":    cty.StringVal("30s"),
	})
	if !out["created"].True() {
		t.Error("created: want true")
	}
	if !out["new_fields"].RawEquals(cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")})) {
		t.Errorf("new_fields: %#v", out["new_fields"])
	}
	if srv.HGet("flags:1", "a") != "true" || srv.HGet("flags:1", "b") != "false" {
		t.Errorf("fields: %q %q", srv.HGet("flags:1", "a"), srv.HGet("flags:1", "b"))
	}
	if srv.TTL("flags:1") != 30*time.Second {
		t.Errorf("ttl: %v", srv.TTL("flags:1"))
	}

	ignore := func(key string) map[string]cty.Value {
		return exec(t, p, "redis_hset", map[string]cty.Value{
			"key":         cty.StringVal(key),
			"fields":      cty.ObjectVal(map[string]cty.Value{"c": cty.StringVal("x")}),
			"on_conflict": cty.StringVal("ignore"),
		})
	}
	if out := ignore("flags:1"); out["created"].True() || out["new_fields"].LengthInt() != 0 {
		t.Errorf("ignore existing: %#v", out)
	}
	if srv.HGet("flags:1", "c") != "" {
		t.Error("ignore wrote to an existing hash")
	}
	if out := ignore("flags:2"); !out["created"].True() || out["new_fields"].LengthInt() != 1 {
		t.Errorf("ignore new: %#v", out)
	}
}

func TestCommand(t *testing.T) {
	p, _ := configured(t)
	out := exec(t, p, "redis_command", map[string]cty.Value{
		"args": cty.TupleVal([]cty.Value{cty.StringVal("RPUSH"), cty.StringVal("q"), cty.NumberIntVal(1), cty.True}),
	})
	if !out["result"].RawEquals(cty.NumberIntVal(2)) {
		t.Errorf("RPUSH: %#v", out["result"])
	}
	out = exec(t, p, "redis_command", map[string]cty.Value{
		"args": cty.TupleVal([]cty.Value{cty.StringVal("LRANGE"), cty.StringVal("q"), cty.NumberIntVal(0), cty.NumberIntVal(-1)}),
	})
	want := cty.TupleVal([]cty.Value{cty.StringVal("1"), cty.StringVal("true")})
	if !out["result"].RawEquals(want) {
		t.Errorf("LRANGE: %#v", out["result"])
	}
	out = exec(t, p, "redis_command", map[string]cty.Value{
		"args": cty.TupleVal([]cty.Value{cty.StringVal("GET"), cty.StringVal("missing")}),
	})
	if !out["result"].IsNull() {
		t.Errorf("GET missing: %#v", out["result"])
	}
	if _, err := p.Execute(context.Background(), "redis_command", map[string]cty.Value{
		"args": cty.TupleVal([]cty.Value{cty.StringVal("NOPE")}),
	}); err == nil {
		t.Error("error reply: want error")
	}
}

func TestPlanActionChecks(t *testing.T) {
	p := New()
	cases := []struct {
		action string
		attrs  map[string]cty.Value
		want   string
	}{
		{"redis_command", map[string]cty.Value{"args": cty.EmptyTupleVal}, "no command"},
		{"redis_command", map[string]cty.Value{"args": cty.StringVal("GET k")}, "must be a list"},
		{"redis_command", map[string]cty.Value{"args": cty.TupleVal([]cty.Value{cty.StringVal("SET"), cty.EmptyObjectVal})}, "jsonencode"},
		{"redis_set", map[string]cty.Value{"key": cty.StringVal("k"), "value": cty.NullVal(cty.String)}, "null"},
		{"redis_set", map[string]cty.Value{"key": cty.StringVal("k"), "value": cty.True, "ttl": cty.StringVal("1 hour")}, "duration"},
		{"redis_set", map[string]cty.Value{"key": cty.StringVal("k"), "value": cty.True, "ttl": cty.StringVal("-1s")}, "positive"},
		{"redis_set", map[string]cty.Value{"key": cty.StringVal("k"), "value": cty.True, "on_conflict": cty.StringVal("error")}, "on_conflict"},
		{"redis_hset", map[string]cty.Value{"key": cty.StringVal("k"), "fields": cty.EmptyObjectVal}, "no fields"},
		{"redis_hset", map[string]cty.Value{"key": cty.StringVal("k"), "fields": cty.ObjectVal(map[string]cty.Value{"a": cty.NullVal(cty.String)})}, "null"},
		{"redis_hset", map[string]cty.Value{"key": cty.StringVal("k"), "fields": cty.StringVal("a")}, "object"},
	}
	for _, c := range cases {
		_, err := p.PlanAction(context.Background(), c.action, c.attrs)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %v: got %v, want %q", c.action, c.attrs, err, c.want)
		}
	}
	// Unknowns are skipped.
	if _, err := p.PlanAction(context.Background(), "redis_command", map[string]cty.Value{"args": cty.DynamicVal}); err != nil {
		t.Errorf("unknown args: %v", err)
	}
}

func TestEncodeValue(t *testing.T) {
	cases := map[string]cty.Value{
		"x":                    cty.StringVal("x"),
		"42":                   cty.NumberIntVal(42),
		"0.1":                  cty.MustParseNumberVal("0.1"),
		"12345678901234567890": cty.MustParseNumberVal("12345678901234567890"),
		"false":                cty.False,
		`["a",1]`:              cty.TupleVal([]cty.Value{cty.StringVal("a"), cty.NumberIntVal(1)}),
		`{"a":{"b":true}}`:     cty.ObjectVal(map[string]cty.Value{"a": cty.ObjectVal(map[string]cty.Value{"b": cty.True})}),
	}
	for want, v := range cases {
		if got, err := encodeValue(v); err != nil || got != want {
			t.Errorf("encode %#v: got %q, %v; want %q", v, got, err, want)
		}
	}
}

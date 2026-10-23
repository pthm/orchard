package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/provider/grpc/grpctest"
	"github.com/pthm/orchard/pkg/snapshot"
)

const descriptorSet = "grpctest/testdata/accounts.binpb"

func configured(t *testing.T, opts grpctest.Options, cfg map[string]cty.Value) *Provider {
	t.Helper()
	addr, stop, err := grpctest.Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if cfg == nil {
		cfg = map[string]cty.Value{}
	}
	cfg["address"] = cty.StringVal(addr)
	p := New()
	if err := p.Configure(context.Background(), cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func obj(m map[string]cty.Value) cty.Value { return cty.ObjectVal(m) }

func call(t *testing.T, p *Provider, method string, request cty.Value, extra map[string]cty.Value) map[string]cty.Value {
	t.Helper()
	attrs := map[string]cty.Value{"method": cty.StringVal(method), "request": request}
	for k, v := range extra {
		attrs[k] = v
	}
	plan, err := p.PlanAction(context.Background(), "grpc_call", attrs)
	if err != nil {
		t.Fatalf("PlanAction: %v", err)
	}
	out, err := p.Execute(context.Background(), "grpc_call", attrs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The outputs must fit the type PlanAction promised.
	if errs := cty.ObjectVal(out).Type().TestConformance(plan.Outputs); len(errs) > 0 {
		t.Errorf("outputs don't conform to the planned type: %v", errs)
	}
	return out
}

func TestSchema(t *testing.T) {
	if _, ok := New().Schema().Actions["grpc_call"]; !ok {
		t.Fatal("missing grpc_call")
	}
}

func TestCreateOrgOverReflection(t *testing.T) {
	p := configured(t, grpctest.Options{}, map[string]cty.Value{
		"default_metadata": cty.MapVal(map[string]cty.Value{"authorization": cty.StringVal("Bearer default")}),
	})
	out := call(t, p, "accounts.v1.Accounts/CreateOrg", obj(map[string]cty.Value{
		"name":  cty.StringVal("Acme"),
		"plan":  cty.StringVal("PLAN_TEAM"),
		"owner": obj(map[string]cty.Value{"email": cty.StringVal("a@example.com")}),
		"email": cty.StringVal("ops@example.com"),
	}), map[string]cty.Value{
		"metadata": cty.MapVal(map[string]cty.Value{"authorization": cty.StringVal("Bearer call")}),
	})
	org := out["response"].GetAttr("org")
	want := map[string]cty.Value{
		"id":         cty.StringVal("org-1"),
		"name":       cty.StringVal("Acme"),
		"plan":       cty.StringVal("PLAN_TEAM"),
		"created_at": cty.StringVal(grpctest.CreatedAt.Format(time.RFC3339Nano)),
	}
	for k, v := range want {
		if !org.GetAttr(k).RawEquals(v) {
			t.Errorf("org.%s: got %#v, want %#v", k, org.GetAttr(k), v)
		}
	}
	if !org.GetAttr("owner").GetAttr("age").RawEquals(cty.NumberIntVal(0)) {
		t.Errorf("unset scalar should be zero: %#v", org.GetAttr("owner"))
	}
	if got := out["headers"].Index(cty.StringVal("x-auth")).AsString(); got != "Bearer call" {
		t.Errorf("call metadata should override the default: %q", got)
	}
	if got := out["trailers"].Index(cty.StringVal("x-trace-bin")).AsString(); got != "AAEC" {
		t.Errorf("binary trailer: %q", got)
	}

	// grpcurl's dotted form works too.
	out = call(t, p, "accounts.v1.Accounts.GetOrg", obj(map[string]cty.Value{"org_id": cty.StringVal("org-1")}), nil)
	if out["response"].GetAttr("name").AsString() != "Acme" {
		t.Errorf("GetOrg: %#v", out["response"])
	}
}

func TestV1AlphaReflection(t *testing.T) {
	p := configured(t, grpctest.Options{V1AlphaReflection: true}, nil)
	out := call(t, p, "accounts.v1.Accounts/CreateOrg", obj(map[string]cty.Value{"name": cty.StringVal("Acme")}), nil)
	if out["response"].GetAttr("org").GetAttr("id").AsString() != "org-1" {
		t.Errorf("response: %#v", out["response"])
	}
}

func TestEchoAllTypes(t *testing.T) {
	p := configured(t, grpctest.Options{}, nil)
	owner := obj(map[string]cty.Value{"email": cty.StringVal("a@b.c"), "age": cty.NumberIntVal(30)})
	req := map[string]cty.Value{
		"i32": cty.NumberIntVal(-1), "s32": cty.NumberIntVal(-2), "sf32": cty.NumberIntVal(3),
		"u32": cty.NumberIntVal(4), "f32": cty.NumberIntVal(5), "fl": cty.NumberFloatVal(1.5), "db": cty.NumberFloatVal(2.25),
		"i64": cty.MustParseNumberVal("-9007199254740993"), "s64": cty.NumberIntVal(-7), "sf64": cty.NumberIntVal(8),
		"u64": cty.MustParseNumberVal("18446744073709551615"), "f64": cty.NumberIntVal(10),
		"b": cty.True, "s": cty.StringVal("hi"), "by": cty.StringVal("AAEC"), "plan": cty.NumberIntVal(1),
		"owner":  owner,
		"tags":   cty.TupleVal([]cty.Value{cty.StringVal("x"), cty.StringVal("y")}),
		"owners": cty.TupleVal([]cty.Value{owner}),
		"counts": obj(map[string]cty.Value{"a": cty.NumberIntVal(1)}),
		"by_id":  obj(map[string]cty.Value{"7": owner}),
		"number": cty.NumberIntVal(42),
		"maybe":  cty.StringVal("set"),
		"ts":     cty.StringVal("2026-01-02T03:04:05.5Z"),
		"dur":    cty.StringVal("1.5s"),
		"st":     obj(map[string]cty.Value{"k": cty.StringVal("v"), "n": cty.NumberIntVal(1)}),
		"val":    cty.TupleVal([]cty.Value{cty.True, cty.StringVal("x")}),
		"lv":     cty.TupleVal([]cty.Value{cty.NumberIntVal(1)}),
		"any":    obj(map[string]cty.Value{"@type": cty.StringVal("type.googleapis.com/accounts.v1.Owner"), "email": cty.StringVal("z@z.z")}),
		"sw":     cty.StringVal("wrapped"),
		"iw":     cty.NumberIntVal(9),
		"empty":  cty.EmptyObjectVal,
		"mask":   cty.StringVal("name,owner.email"),
		"node": obj(map[string]cty.Value{
			"name":     cty.StringVal("root"),
			"children": cty.TupleVal([]cty.Value{obj(map[string]cty.Value{"name": cty.StringVal("leaf")})}),
		}),
	}
	out := call(t, p, "accounts.v1.Accounts/Echo", obj(req), nil)
	resp := out["response"]
	want := map[string]cty.Value{
		"i64":   cty.MustParseNumberVal("-9007199254740993"),
		"u64":   cty.MustParseNumberVal("18446744073709551615"),
		"plan":  cty.StringVal("PLAN_FREE"),
		"by":    cty.StringVal("AAEC"),
		"ts":    cty.StringVal("2026-01-02T03:04:05.5Z"),
		"dur":   cty.StringVal("1.5s"),
		"mask":  cty.StringVal("name,owner.email"),
		"sw":    cty.StringVal("wrapped"),
		"iw":    cty.NumberIntVal(9),
		"empty": cty.EmptyObjectVal,
		"tags":  cty.ListVal([]cty.Value{cty.StringVal("x"), cty.StringVal("y")}),
		"text":  cty.NullVal(cty.String),
		"maybe": cty.StringVal("set"),
		"bw":    cty.NullVal(cty.Bool),
	}
	for k, v := range want {
		if !resp.GetAttr(k).RawEquals(v) {
			t.Errorf("%s: got %#v, want %#v", k, resp.GetAttr(k), v)
		}
	}
	if got := resp.GetAttr("by_id").Index(cty.StringVal("7")).GetAttr("age"); !got.RawEquals(cty.NumberIntVal(30)) {
		t.Errorf("by_id: %#v", resp.GetAttr("by_id"))
	}
	if got := resp.GetAttr("st").GetAttr("k"); !got.RawEquals(cty.StringVal("v")) {
		t.Errorf("st: %#v", resp.GetAttr("st"))
	}
	if got := resp.GetAttr("any").GetAttr("email"); !got.RawEquals(cty.StringVal("z@z.z")) {
		t.Errorf("any: %#v", resp.GetAttr("any"))
	}
	children := resp.GetAttr("node").GetAttr("children")
	if children.LengthInt() != 1 || children.Index(cty.NumberIntVal(0)).GetAttr("name").AsString() != "leaf" {
		t.Errorf("node: %#v", resp.GetAttr("node"))
	}
}

func TestValidation(t *testing.T) {
	p := configured(t, grpctest.Options{}, nil)
	cases := []struct {
		method  string
		request cty.Value
		want    string
	}{
		{"accounts.v1.Account/CreateOrg", cty.EmptyObjectVal, `no service "accounts.v1.Account" (did you mean "accounts.v1.Accounts"?)`},
		{"accounts.v1.Accounts/CreateOrgs", cty.EmptyObjectVal, `has no method "CreateOrgs" (did you mean "CreateOrg"?)`},
		{"accounts.v1.Accounts/WatchOrgs", cty.EmptyObjectVal, `"WatchOrgs" is server-streaming`},
		{"accounts.v1.Accounts/CreateOrg", obj(map[string]cty.Value{"nme": cty.StringVal("x")}), `accounts.v1.CreateOrgRequest has no field "nme" (did you mean "name"?)`},
		{"accounts.v1.Accounts/CreateOrg", obj(map[string]cty.Value{"owner": obj(map[string]cty.Value{"age": cty.StringVal("x")})}), `field "owner.age" is int32; got string`},
		{"accounts.v1.Accounts/CreateOrg", obj(map[string]cty.Value{"plan": cty.StringVal("PLAN_PRO")}), `field "plan" is accounts.v1.Plan (PLAN_UNSPECIFIED, PLAN_FREE, PLAN_TEAM); got "PLAN_PRO"`},
		{"accounts.v1.Accounts/CreateOrg", obj(map[string]cty.Value{"email": cty.StringVal("a"), "phone": cty.StringVal("b")}), `fields "email" and "phone" are both in oneof "contact"`},
		{"accounts.v1.Accounts/Echo", obj(map[string]cty.Value{"i32": cty.NumberIntVal(1 << 40)}), `is out of range`},
		{"accounts.v1.Accounts/Echo", obj(map[string]cty.Value{"i64": cty.NumberFloatVal(1.5)}), `isn't a whole number`},
		{"accounts.v1.Accounts/Echo", obj(map[string]cty.Value{"ts": cty.StringVal("yesterday")}), `RFC 3339`},
		{"accounts.v1.Accounts/Echo", obj(map[string]cty.Value{"i32": cty.UnknownVal(cty.String)}), `field "i32" is int32; got string`},
	}
	for _, c := range cases {
		_, err := p.PlanAction(context.Background(), "grpc_call", map[string]cty.Value{"method": cty.StringVal(c.method), "request": c.request})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %#v:\n got  %v\n want %s", c.method, c.request, err, c.want)
		}
	}
	// Unknowns of the right (or unknown) type are fine.
	if _, err := p.PlanAction(context.Background(), "grpc_call", map[string]cty.Value{
		"method":  cty.StringVal("accounts.v1.Accounts/CreateOrg"),
		"request": obj(map[string]cty.Value{"name": cty.UnknownVal(cty.String), "owner": cty.DynamicVal}),
	}); err != nil {
		t.Errorf("unknowns: %v", err)
	}
}

func TestStatusErrors(t *testing.T) {
	p := configured(t, grpctest.Options{}, nil)
	_, err := p.Execute(context.Background(), "grpc_call", map[string]cty.Value{
		"method": cty.StringVal("accounts.v1.Accounts/Fail"), "request": cty.EmptyObjectVal,
	})
	if err == nil || !strings.Contains(err.Error(), "INVALID_ARGUMENT: name is required") || !strings.Contains(err.Error(), `"field":"name"`) {
		t.Errorf("Fail: %v", err)
	}
	_, err = p.Execute(context.Background(), "grpc_call", map[string]cty.Value{
		"method": cty.StringVal("accounts.v1.Accounts/GetOrg"), "request": obj(map[string]cty.Value{"org_id": cty.StringVal("nope")}),
	})
	if err == nil || !strings.Contains(err.Error(), `NOT_FOUND: org "nope" doesn't exist`) {
		t.Errorf("GetOrg: %v", err)
	}
}

func TestDescriptorSet(t *testing.T) {
	p := configured(t, grpctest.Options{NoReflection: true}, map[string]cty.Value{"descriptor_set": cty.StringVal(descriptorSet)})
	out := call(t, p, "accounts.v1.Accounts/CreateOrg", obj(map[string]cty.Value{"name": cty.StringVal("Acme")}), nil)
	if out["response"].GetAttr("org").GetAttr("id").AsString() != "org-1" {
		t.Errorf("response: %#v", out["response"])
	}

	noRefl := configured(t, grpctest.Options{NoReflection: true}, nil)
	_, err := noRefl.PlanAction(context.Background(), "grpc_call", map[string]cty.Value{
		"method": cty.StringVal("accounts.v1.Accounts/CreateOrg"), "request": cty.EmptyObjectVal,
	})
	if err == nil || !strings.Contains(err.Error(), "descriptor_set") {
		t.Errorf("no reflection, no descriptor set: %v", err)
	}
}

// TestPlanFromSnapshot pulls a snapshot over reflection, then plans
// with it alone, as `orchard plan` does.
func TestPlanFromSnapshot(t *testing.T) {
	live := configured(t, grpctest.Options{}, nil)
	capture, err := live.PullSchema(context.Background(), provider.PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var body snapshotBody
	if err := json.Unmarshal(capture.Body, &body); err != nil {
		t.Fatal(err)
	}
	if strings.Join(body.Services, ",") != "accounts.v1.Accounts" {
		t.Errorf("services: %v", body.Services)
	}
	if !strings.HasPrefix(capture.Target, "grpc/127.0.0.1:") || capture.Kind != "protobuf" {
		t.Errorf("capture: %s %s", capture.Kind, capture.Target)
	}

	planner := New()
	if _, err := planner.PlanAction(context.Background(), "grpc_call", map[string]cty.Value{"method": cty.StringVal("a.B/C")}); !errors.Is(err, provider.ErrNoSchema) {
		t.Errorf("no snapshot: want ErrNoSchema, got %v", err)
	}
	snap := &snapshot.Snapshot{Kind: capture.Kind, Target: capture.Target, Body: capture.Body}
	if err := planner.PlanConfigure(context.Background(), map[string]cty.Value{"address": cty.StringVal("x:1")}, snap); err != nil {
		t.Fatal(err)
	}
	plan, err := planner.PlanAction(context.Background(), "grpc_call", map[string]cty.Value{
		"method": cty.StringVal("accounts.v1.Accounts/CreateOrg"), "request": cty.EmptyObjectVal,
	})
	if err != nil {
		t.Fatal(err)
	}
	org := plan.Outputs.AttributeType("response").AttributeType("org")
	if !org.AttributeType("created_at").Equals(cty.String) || !org.AttributeType("plan").Equals(cty.String) {
		t.Errorf("org type: %#v", org)
	}
	if _, err := planner.PlanAction(context.Background(), "grpc_call", map[string]cty.Value{
		"method": cty.StringVal("accounts.v1.Accounts/CreateOrg"), "request": obj(map[string]cty.Value{"nme": cty.StringVal("x")}),
	}); err == nil {
		t.Error("snapshot plan should catch request typos")
	}
}

func TestCodeName(t *testing.T) {
	for in, want := range map[string]string{"NotFound": "NOT_FOUND", "OK": "OK", "DeadlineExceeded": "DEADLINE_EXCEEDED", "Internal": "INTERNAL"} {
		if got := codeName(in); got != want {
			t.Errorf("%s: got %s", in, got)
		}
	}
}

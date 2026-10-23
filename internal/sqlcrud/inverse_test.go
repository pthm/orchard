package sqlcrud

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/dbschema"
)

func TestInverse(t *testing.T) {
	a := &Actions{Prefix: "postgres", Dialect: Postgres, schema: &dbschema.Schema{
		DefaultSchema: "public",
		Tables: map[string]*dbschema.Table{
			"public.merchants": {Schema: "public", Name: "merchants", PrimaryKey: []string{"id"}},
			"public.tags":      {Schema: "public", Name: "tags", PrimaryKey: []string{"product_id", "tag"}},
			"public.log":       {Schema: "public", Name: "log"},
		},
	}}
	table := func(name string) map[string]cty.Value { return map[string]cty.Value{"table": cty.StringVal(name)} }

	inv := a.Inverse("postgres_insert", table("merchants"), map[string]cty.Value{"id": cty.NumberIntVal(7), "name": cty.StringVal("Acme")})
	if inv == nil || inv.Action != "postgres_delete" || !inv.Attrs["table"].RawEquals(cty.StringVal("public.merchants")) ||
		!inv.Attrs["where"].RawEquals(cty.ObjectVal(map[string]cty.Value{"id": cty.NumberIntVal(7)})) {
		t.Fatalf("insert undo: %#v", inv)
	}

	inv = a.Inverse("postgres_insert", table("tags"), map[string]cty.Value{"product_id": cty.NumberIntVal(1), "tag": cty.StringVal("gift")})
	if inv == nil || !inv.Attrs["where"].RawEquals(cty.ObjectVal(map[string]cty.Value{
		"product_id": cty.NumberIntVal(1), "tag": cty.StringVal("gift"),
	})) {
		t.Fatalf("composite key undo: %#v", inv)
	}

	for name, c := range map[string]struct {
		action  string
		table   string
		outputs map[string]cty.Value
	}{
		"update":           {"postgres_update", "merchants", map[string]cty.Value{"rows_affected": cty.NumberIntVal(1)}},
		"no primary key":   {"postgres_insert", "log", map[string]cty.Value{"msg": cty.StringVal("x")}},
		"key missing":      {"postgres_insert", "merchants", map[string]cty.Value{"name": cty.StringVal("x")}},
		"key null":         {"postgres_insert", "merchants", map[string]cty.Value{"id": cty.NullVal(cty.Number)}},
		"unknown table":    {"postgres_insert", "nope", map[string]cty.Value{"id": cty.NumberIntVal(1)}},
		"another provider": {"mysql_insert", "merchants", map[string]cty.Value{"id": cty.NumberIntVal(1)}},
	} {
		if inv := a.Inverse(c.action, table(c.table), c.outputs); inv != nil {
			t.Errorf("%s: expected no undo, got %#v", name, inv)
		}
	}

	if inv := (&Actions{Prefix: "postgres"}).Inverse("postgres_insert", table("merchants"), map[string]cty.Value{"id": cty.NumberIntVal(1)}); inv != nil {
		t.Errorf("no schema loaded: expected no undo, got %#v", inv)
	}
}

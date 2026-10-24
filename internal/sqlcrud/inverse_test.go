package sqlcrud

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/provider"
)

func TestUndo(t *testing.T) {
	a := &Actions{Prefix: "postgres", Dialect: Postgres}
	merchants := &dbschema.Table{Schema: "public", Name: "merchants", PrimaryKey: []string{"id"}}
	tags := &dbschema.Table{Schema: "public", Name: "tags", PrimaryKey: []string{"product_id", "tag"}}
	log := &dbschema.Table{Schema: "public", Name: "log"}
	inserted := change{inserted: true}

	inv := a.undo(merchants, inserted, map[string]cty.Value{"id": cty.NumberIntVal(7), "name": cty.StringVal("Acme")}, provider.InverseOptions{})
	if inv == nil || inv.Action != "postgres_delete" || !inv.Attrs["table"].RawEquals(cty.StringVal("public.merchants")) ||
		!inv.Attrs["where"].RawEquals(cty.ObjectVal(map[string]cty.Value{"id": cty.NumberIntVal(7)})) {
		t.Fatalf("insert undo: %#v", inv)
	}

	inv = a.undo(tags, inserted, map[string]cty.Value{"product_id": cty.NumberIntVal(1), "tag": cty.StringVal("gift")}, provider.InverseOptions{})
	if inv == nil || !inv.Attrs["where"].RawEquals(cty.ObjectVal(map[string]cty.Value{
		"product_id": cty.NumberIntVal(1), "tag": cty.StringVal("gift"),
	})) {
		t.Fatalf("composite key undo: %#v", inv)
	}

	for name, c := range map[string]struct {
		table   *dbschema.Table
		change  change
		outputs map[string]cty.Value
	}{
		"update, nothing matched": {merchants, change{}, map[string]cty.Value{"rows_affected": cty.NumberIntVal(0)}},
		"upsert that ignored":     {merchants, change{}, map[string]cty.Value{"id": cty.NumberIntVal(1)}},
		"no primary key":          {log, inserted, map[string]cty.Value{"msg": cty.StringVal("x")}},
		"key missing":             {merchants, inserted, map[string]cty.Value{"name": cty.StringVal("x")}},
		"key null":                {merchants, inserted, map[string]cty.Value{"id": cty.NullVal(cty.Number)}},
	} {
		if inv := a.undo(c.table, c.change, c.outputs, provider.InverseOptions{}); inv != nil {
			t.Errorf("%s: expected no undo, got %#v", name, inv)
		}
	}

	row := []map[string]cty.Value{{"id": cty.NumberIntVal(1), "name": cty.StringVal("old")}}
	unowned := change{update: &updated{table: merchants, rows: row}}
	if inv := a.undo(merchants, unowned, nil, provider.InverseOptions{}); inv != nil {
		t.Errorf("unowned update, not requested: expected no undo, got %#v", inv)
	}
	if inv := a.undo(merchants, unowned, nil, provider.InverseOptions{Requested: true}); inv == nil || inv.Action != "postgres_restore" {
		t.Errorf("requested update: expected a restore, got %#v", inv)
	}
}

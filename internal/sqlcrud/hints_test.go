package sqlcrud

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/dbschema"
)

func hintActions() *Actions {
	return &Actions{Prefix: "postgres", Dialect: Postgres, Strict: true, schema: &dbschema.Schema{
		Tables: map[string]*dbschema.Table{
			"merchants": {Name: "merchants", Columns: []dbschema.Column{{Name: "id", Kind: dbschema.KindInteger, HasDefault: true}, {Name: "name", Kind: dbschema.KindText}}, PrimaryKey: []string{"id"}},
			"products": {
				Name:        "products",
				Columns:     []dbschema.Column{{Name: "id", Kind: dbschema.KindInteger, HasDefault: true}, {Name: "merchant_id", Kind: dbschema.KindInteger}},
				PrimaryKey:  []string{"id"},
				ForeignKeys: []dbschema.ForeignKey{{Columns: []string{"merchant_id"}, References: "merchants", ReferencedColumns: []string{"id"}}},
			},
		},
	}}
}

func plan(t *testing.T, a *Actions, action, table string, attrs map[string]cty.Value) []string {
	t.Helper()
	attrs["table"] = cty.StringVal(table)
	p, err := a.Plan(context.Background(), action, attrs)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return p.Hints
}

func TestForeignKeyHints(t *testing.T) {
	product := func(v cty.Value) map[string]cty.Value {
		return map[string]cty.Value{"values": cty.ObjectVal(map[string]cty.Value{"merchant_id": v})}
	}

	// No merchant inserted yet: a fixed id is presumably an existing row.
	a := hintActions()
	if h := plan(t, a, "postgres_insert", "products", product(cty.NumberIntVal(1))); len(h) != 0 {
		t.Fatalf("no hint expected before a merchants insert: %v", h)
	}

	plan(t, a, "postgres_insert", "merchants", map[string]cty.Value{"values": cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("Acme")})})

	h := plan(t, a, "postgres_insert", "products", product(cty.NumberIntVal(1)))
	if len(h) != 1 || !strings.Contains(h[0], "merchant_id is set to a fixed value (1), but it references merchants.id") ||
		!strings.Contains(h[0], "action.postgres_insert.<name>.id") {
		t.Fatalf("fixed foreign key after a merchants insert: %v", h)
	}
	if h := plan(t, a, "postgres_insert", "products", product(cty.UnknownVal(cty.Number))); len(h) != 0 {
		t.Fatalf("a reference (unknown at plan time) shouldn't be flagged: %v", h)
	}

	h = plan(t, a, "postgres_update", "products", map[string]cty.Value{
		"set":   cty.ObjectVal(map[string]cty.Value{"merchant_id": cty.NumberIntVal(2)}),
		"where": cty.ObjectVal(map[string]cty.Value{"id": cty.NumberIntVal(5)}),
	})
	if len(h) != 1 {
		t.Fatalf("an update setting a fixed foreign key should be flagged too: %v", h)
	}
}

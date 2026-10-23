package dbschema

import (
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func sampleSchema() *Schema {
	return &Schema{
		Dialect:       "postgres",
		DefaultSchema: "public",
		Tables: map[string]*Table{
			"public.merchants": {
				Schema: "public", Name: "merchants",
				Columns: []Column{
					{Name: "id", Kind: KindInteger, HasDefault: true, Generated: GeneratedIdentity},
					{Name: "name", Kind: KindText},
					{Name: "region", Kind: KindEnum, Enum: []string{"us", "eu"}, HasDefault: true},
					{Name: "balance", Kind: KindDecimal, Nullable: true},
					{Name: "active", Kind: KindBoolean, HasDefault: true},
					{Name: "settings", Kind: KindJSON, Nullable: true},
					{Name: "slug", Kind: KindText, Nullable: true, Generated: GeneratedStored},
					{Name: "seats", Kind: KindInteger, Nullable: true},
				},
				PrimaryKey: []string{"id"},
			},
			"billing.invoices": {Schema: "billing", Name: "invoices", Columns: []Column{{Name: "id", Kind: KindInteger}}},
		},
	}
}

func TestLookup(t *testing.T) {
	s := sampleSchema()
	for _, name := range []string{"merchants", "public.merchants"} {
		if tb, err := s.Lookup(name); err != nil || tb.Name != "merchants" {
			t.Errorf("Lookup(%q) = %v, %v", name, tb, err)
		}
	}
	if tb, err := s.Lookup("billing.invoices"); err != nil || tb.Name != "invoices" {
		t.Errorf("qualified lookup: %v, %v", tb, err)
	}

	_, err := s.Lookup("merchant")
	if !errors.Is(err, ErrNoTable) || !strings.Contains(err.Error(), `no such table "merchant" (did you mean "merchants"?)`) {
		t.Errorf("unqualified miss: %v", err)
	}
	_, err = s.Lookup("billing.invoice")
	if err == nil || !strings.Contains(err.Error(), `did you mean "billing.invoices"?`) {
		t.Errorf("qualified miss: %v", err)
	}
	if _, err := s.Lookup("zzzzzz"); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("a far-off name shouldn't get a suggestion: %v", err)
	}
}

func TestCheckInsert(t *testing.T) {
	tb, _ := sampleSchema().Lookup("merchants")
	obj := cty.ObjectVal

	cases := []struct {
		name   string
		values cty.Value
		want   []string // substrings; none means valid
	}{
		{"minimal", obj(map[string]cty.Value{"name": cty.StringVal("Acme")}), nil},
		{"all writable", obj(map[string]cty.Value{
			"name": cty.StringVal("Acme"), "region": cty.StringVal("eu"), "balance": cty.NumberFloatVal(9.99),
			"active": cty.True, "settings": cty.ObjectVal(map[string]cty.Value{"a": cty.NumberIntVal(1)}),
			"seats": cty.NullVal(cty.Number),
		}), nil},
		{"string converts to integer", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "seats": cty.StringVal("5")}), nil},
		{"number converts to text", obj(map[string]cty.Value{"name": cty.NumberIntVal(5)}), nil},
		{"unknown typed values", obj(map[string]cty.Value{"name": cty.UnknownVal(cty.String), "seats": cty.UnknownVal(cty.Number)}), nil},
		{"unknown untyped value", obj(map[string]cty.Value{"name": cty.DynamicVal}), nil},
		{"whole object unknown", cty.DynamicVal, nil},
		{"unknown object of known shape", cty.UnknownVal(cty.Object(map[string]cty.Type{"name": cty.String})), nil},

		{"typo", obj(map[string]cty.Value{"nme": cty.StringVal("Acme")}), []string{`no column "nme" (did you mean "name"?)`, `missing required column "name"`}},
		{"missing required", obj(map[string]cty.Value{"region": cty.StringVal("us")}), []string{`missing required column "name"`}},
		{"generated", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "slug": cty.StringVal("x")}), []string{`column "slug" is generated`}},
		{"identity always", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "id": cty.NumberIntVal(1)}), []string{`column "id" is generated`}},
		{"wrong type", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "balance": cty.True}), []string{`column "balance" is decimal; got bool`}},
		{"unknown of wrong type", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "active": cty.UnknownVal(cty.List(cty.String))}), []string{`column "active" is boolean`}},
		{"unconvertible string", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "seats": cty.StringVal("many")}), []string{`column "seats" is integer; got string`}},
		{"fractional integer", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "seats": cty.NumberFloatVal(2.5)}), []string{`column "seats" is integer; got 2.5`}},
		{"enum member", obj(map[string]cty.Value{"name": cty.StringVal("Acme"), "region": cty.StringVal("uk")}), []string{`column "region" is enum (us, eu); got "uk"`}},
		{"null in not-null", obj(map[string]cty.Value{"name": cty.NullVal(cty.String)}), []string{`column "name" is NOT NULL; got null`}},
		{"object into text", obj(map[string]cty.Value{"name": cty.ObjectVal(map[string]cty.Value{"a": cty.True})}), []string{`column "name" is text`}},
		{"empty", cty.EmptyObjectVal, []string{"values names no columns"}},
		{"not an object", cty.StringVal("name=Acme"), []string{"values must be an object of column = value, got string"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckInsert(tb, c.values)
			if len(c.want) == 0 {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected errors %q, got none", c.want)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q is missing %q", err, w)
				}
			}
		})
	}
}

func TestCheckUpdateAndDelete(t *testing.T) {
	tb, _ := sampleSchema().Lookup("merchants")
	id := cty.ObjectVal(map[string]cty.Value{"id": cty.UnknownVal(cty.Number)})

	if err := CheckUpdate(tb, cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("eu")}), id); err != nil {
		t.Errorf("valid update: %v", err)
	}
	// where may name generated columns; set may not.
	if err := CheckUpdate(tb, cty.ObjectVal(map[string]cty.Value{"slug": cty.StringVal("x")}), id); err == nil ||
		!strings.Contains(err.Error(), `set: column "slug" is generated`) {
		t.Errorf("generated set: %v", err)
	}
	if err := CheckUpdate(tb, cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("x")}), cty.EmptyObjectVal); err == nil ||
		!strings.Contains(err.Error(), "where names no columns") {
		t.Errorf("empty where: %v", err)
	}
	if err := CheckDelete(tb, cty.ObjectVal(map[string]cty.Value{"idd": cty.NumberIntVal(1)})); err == nil ||
		!strings.Contains(err.Error(), `where: no column "idd" (did you mean "id"?)`) {
		t.Errorf("delete typo: %v", err)
	}
	if err := CheckDelete(tb, id); err != nil {
		t.Errorf("valid delete: %v", err)
	}
}

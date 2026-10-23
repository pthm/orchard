package dbschema

import (
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	old := sampleSchema()
	cur := sampleSchema()

	if d := Diff(old, cur); len(d) != 0 {
		t.Fatalf("identical schemas: %v", d)
	}

	m := cur.Tables["public.merchants"]
	m.Comment = "comments don't count"
	m.Columns[1].Type = "varchar(200)"
	m.Columns[1].Nullable = true
	m.Columns[2].Enum = []string{"us", "eu", "apac"}
	m.Columns[3].HasDefault = true
	m.Columns = append(m.Columns, Column{Name: "status", Kind: KindText})
	m.Columns = slicesDelete(m.Columns, "seats")
	m.ForeignKeys = []ForeignKey{{Columns: []string{"id"}, References: "billing.invoices", ReferencedColumns: []string{"id"}}}
	delete(cur.Tables, "billing.invoices")
	cur.Tables["public.events"] = &Table{Schema: "public", Name: "events"}

	got := strings.Join(Diff(old, cur), "\n")
	want := strings.Join([]string{
		`table "billing.invoices" removed`,
		`table "public.events" added`,
		`column "public.merchants.balance": default added`,
		`column "public.merchants.name": type  → varchar(200); now nullable`,
		`column "public.merchants.region": enum values (us, eu) → (us, eu, apac)`,
		`column "public.merchants.seats" removed`,
		`column "public.merchants.status" added`,
		`foreign keys of "public.merchants" changed`,
	}, "\n")
	if got != want {
		t.Fatalf("diff:\n%s\n\nwant:\n%s", got, want)
	}
}

func slicesDelete(cols []Column, name string) []Column {
	out := cols[:0:0]
	for _, c := range cols {
		if c.Name != name {
			out = append(out, c)
		}
	}
	return out
}

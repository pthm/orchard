package sqlcrud

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	_ "modernc.org/sqlite"

	"github.com/pthm/orchard/pkg/dbschema"
)

// The table's identifiers include a reserved word and a quote so
// quoting is exercised; its columns cover each kind's binding and
// decoding.
const ddl = `
CREATE TABLE "order" (
	id        INTEGER PRIMARY KEY,
	"na""me"  TEXT NOT NULL,
	total     DECIMAL(10,2),
	ratio     REAL,
	paid      BOOLEAN NOT NULL DEFAULT 0,
	meta      JSON,
	blob      BLOB,
	placed_on DATE,
	note      TEXT
);
CREATE TABLE tags (order_id INTEGER NOT NULL, tag TEXT NOT NULL, PRIMARY KEY (order_id, tag));
CREATE TABLE log (msg TEXT);
`

var table = &dbschema.Table{
	Name: "order",
	Columns: []dbschema.Column{
		{Name: "id", Kind: dbschema.KindInteger, HasDefault: true},
		{Name: `na"me`, Kind: dbschema.KindText},
		{Name: "total", Kind: dbschema.KindDecimal, Nullable: true},
		{Name: "ratio", Kind: dbschema.KindFloat, Nullable: true},
		{Name: "paid", Kind: dbschema.KindBoolean, HasDefault: true},
		{Name: "meta", Kind: dbschema.KindJSON, Nullable: true},
		{Name: "blob", Kind: dbschema.KindBinary, Nullable: true},
		{Name: "placed_on", Kind: dbschema.KindDate, Nullable: true},
		{Name: "note", Kind: dbschema.KindText, Nullable: true},
	},
	PrimaryKey: []string{"id"},
}

func TestInsertReturnsTypedRow(t *testing.T) {
	// MySQL's read-back path runs here too, by turning RETURNING off.
	for name, d := range map[string]Dialect{"returning": SQLite, "read back": {Placeholder: question, Quote: doubleQuote}} {
		t.Run(name, func(t *testing.T) {
			conn := openConn(t)
			row, err := Insert(context.Background(), conn, d, table, cty.ObjectVal(map[string]cty.Value{
				`na"me`:     cty.StringVal("O'Brien"),
				"total":     cty.MustParseNumberVal("19.99"),
				"ratio":     cty.NumberFloatVal(0.5),
				"paid":      cty.True,
				"meta":      cty.ObjectVal(map[string]cty.Value{"gift": cty.True, "count": cty.NumberIntVal(2)}),
				"blob":      cty.StringVal("raw"),
				"placed_on": cty.StringVal("2026-10-23"),
				"note":      cty.NullVal(cty.String),
			}))
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			want := map[string]cty.Value{
				"id":    cty.NumberIntVal(1),
				`na"me`: cty.StringVal("O'Brien"),
				"total": cty.MustParseNumberVal("19.99"),
				"ratio": cty.NumberFloatVal(0.5),
				"paid":  cty.True,
				"meta":  cty.ObjectVal(map[string]cty.Value{"gift": cty.True, "count": cty.NumberIntVal(2)}),
				"blob":  cty.StringVal("raw"),
				"note":  cty.NullVal(cty.DynamicPseudoType),
			}
			for k, w := range want {
				if got := row[k]; !got.RawEquals(w) {
					t.Errorf("%s: got %#v want %#v", k, got, w)
				}
			}
			if !strings.HasPrefix(row["placed_on"].AsString(), "2026-10-23") {
				t.Errorf("placed_on: %#v", row["placed_on"])
			}
		})
	}
}

func TestReadBackCompositeKeyAndNoKey(t *testing.T) {
	conn := openConn(t)
	readBackDialect := Dialect{Placeholder: question, Quote: doubleQuote}

	tags := &dbschema.Table{
		Name:       "tags",
		Columns:    []dbschema.Column{{Name: "order_id", Kind: dbschema.KindInteger}, {Name: "tag", Kind: dbschema.KindText}},
		PrimaryKey: []string{"order_id", "tag"},
	}
	row, err := Insert(context.Background(), conn, readBackDialect, tags, cty.ObjectVal(map[string]cty.Value{
		"order_id": cty.NumberIntVal(7), "tag": cty.StringVal("gift"),
	}))
	if err != nil || !row["tag"].RawEquals(cty.StringVal("gift")) || !row["order_id"].RawEquals(cty.NumberIntVal(7)) {
		t.Fatalf("composite key read-back: %#v %v", row, err)
	}

	log := &dbschema.Table{Name: "log", Columns: []dbschema.Column{{Name: "msg", Kind: dbschema.KindText, Nullable: true}}}
	row, err = Insert(context.Background(), conn, readBackDialect, log, cty.ObjectVal(map[string]cty.Value{"msg": cty.StringVal("hi")}))
	if err != nil || !row["msg"].RawEquals(cty.StringVal("hi")) || !row["last_insert_id"].RawEquals(cty.NumberIntVal(1)) {
		t.Fatalf("keyless table: %#v %v", row, err)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	conn := openConn(t)
	for _, n := range []string{"a", "b", "c"} {
		if _, err := Insert(ctx, conn, SQLite, table, cty.ObjectVal(map[string]cty.Value{`na"me`: cty.StringVal(n)})); err != nil {
			t.Fatal(err)
		}
	}

	out, err := Update(ctx, conn, SQLite, table,
		cty.ObjectVal(map[string]cty.Value{"paid": cty.True, "note": cty.StringVal("done")}),
		cty.ObjectVal(map[string]cty.Value{"note": cty.NullVal(cty.String), "paid": cty.False}))
	if err != nil || !out["rows_affected"].RawEquals(cty.NumberIntVal(3)) {
		t.Fatalf("update with an IS NULL condition: %#v %v", out, err)
	}

	out, err = Delete(ctx, conn, SQLite, table, cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("2")}))
	if err != nil || !out["rows_affected"].RawEquals(cty.NumberIntVal(1)) {
		t.Fatalf("delete by a string id: %#v %v", out, err)
	}

	if _, err := Delete(ctx, conn, SQLite, table, cty.EmptyObjectVal); err == nil {
		t.Fatal("an empty where must be refused")
	}
}

func TestBindRejectsUnknown(t *testing.T) {
	if _, err := bind(&table.Columns[1], cty.UnknownVal(cty.String)); err == nil {
		t.Fatal("expected an error for an unknown value")
	}
}

func TestBindIntegerBeyondInt64(t *testing.T) {
	big := cty.MustParseNumberVal("18446744073709551616")
	got, err := bind(&dbschema.Column{Name: "n", Kind: dbschema.KindInteger}, big)
	if err != nil || got != "18446744073709551616" {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestQuote(t *testing.T) {
	if got := doubleQuote(`a"b`); got != `"a""b"` {
		t.Errorf("doubleQuote: %s", got)
	}
	if got := backtick("a`b"); got != "`a``b`" {
		t.Errorf("backtick: %s", got)
	}
	if got := tableRef(Postgres, &dbschema.Table{Schema: "billing", Name: "invoices"}); got != `"billing"."invoices"` {
		t.Errorf("tableRef: %s", got)
	}
}

func openConn(t *testing.T) *sql.Conn {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(context.Background(), ddl); err != nil {
		t.Fatal(err)
	}
	return conn
}

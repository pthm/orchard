package sqlrow

import (
	"context"
	"database/sql"
	"math/big"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
	_ "modernc.org/sqlite"
)

func TestToCty(t *testing.T) {
	ts := time.Date(2026, 10, 23, 10, 30, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   any
		want cty.Value
	}{
		{"nil", nil, cty.NullVal(cty.DynamicPseudoType)},
		{"string", "hello", cty.StringVal("hello")},
		{"[]byte", []byte("raw"), cty.StringVal("raw")},
		{"bool", true, cty.BoolVal(true)},
		{"int", 3, cty.NumberIntVal(3)},
		{"int16", int16(5), cty.NumberIntVal(5)},
		{"int32", int32(7), cty.NumberIntVal(7)},
		{"int64", int64(42), cty.NumberIntVal(42)},
		{"uint64", uint64(18446744073709551615), cty.NumberUIntVal(18446744073709551615)},
		{"float32", float32(1.5), cty.NumberFloatVal(1.5)},
		{"float64", 3.14, cty.NumberFloatVal(3.14)},
		{"big.Float", big.NewFloat(2.5), cty.NumberFloatVal(2.5)},
		{"time", ts, cty.StringVal("2026-10-23T10:30:00Z")},
		{"fallback", struct{ X int }{5}, cty.StringVal("{5}")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToCty(c.in); !got.RawEquals(c.want) {
				t.Fatalf("got %#v want %#v", got, c.want)
			}
		})
	}
}

func TestColumnValueDecodesNumericBytes(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		dbType string
		want   cty.Value
	}{
		{"int bytes", []byte("42"), "INT", cty.NumberIntVal(42)},
		{"unsigned bigint bytes", []byte("7"), "UNSIGNED BIGINT", cty.NumberIntVal(7)},
		{"decimal bytes", []byte("9.99"), "DECIMAL", cty.MustParseNumberVal("9.99")},
		{"lowercase type", []byte("1"), "integer", cty.NumberIntVal(1)},
		{"text bytes stay strings", []byte("42"), "VARCHAR", cty.StringVal("42")},
		{"point is not int", []byte("x"), "POINT", cty.StringVal("x")},
		{"unparseable falls back", []byte("n/a"), "INT", cty.StringVal("n/a")},
		{"typed values pass through", int64(3), "INT", cty.NumberIntVal(3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := columnValue(c.in, c.dbType); !got.RawEquals(c.want) {
				t.Fatalf("got %#v want %#v", got, c.want)
			}
		})
	}
}

// Query's two output shapes, exercised through SQLite (no server
// needed). The mysql and sqlite provider tests cover their own status
// queries end to end.
func TestQuery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	const status = "SELECT last_insert_rowid(), changes()"
	run := func(q string) map[string]cty.Value {
		t.Helper()
		out, err := Query(context.Background(), conn, q, status)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return out
	}

	run("CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)")

	out := run("INSERT INTO t (name) VALUES ('a')")
	if !out["last_insert_id"].RawEquals(cty.NumberIntVal(1)) || !out["rows_affected"].RawEquals(cty.NumberIntVal(1)) {
		t.Fatalf("no-rows outputs: %#v", out)
	}

	out = run("INSERT INTO t (name) VALUES ('b') RETURNING id, name")
	if !out["id"].RawEquals(cty.NumberIntVal(2)) || !out["name"].RawEquals(cty.StringVal("b")) || len(out) != 2 {
		t.Fatalf("row outputs: %#v", out)
	}

	if out := run("SELECT id FROM t WHERE id = 99"); len(out) != 0 {
		t.Fatalf("empty select: %#v", out)
	}

	if _, err := Query(context.Background(), conn, "SELEC nope", status); err == nil {
		t.Fatal("expected an execute error")
	}
}

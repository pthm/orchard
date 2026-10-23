package sqlite

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/pkg/dbschema"
	"github.com/pthm/orchard/pkg/provider"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/schema.golden.json")

// sampleSchema covers each case introspection has to get right: rowid
// and composite keys, WITHOUT ROWID, defaults, stored and virtual
// generated columns, every kind, untyped columns, foreign keys with
// and without referenced columns, and a view (which must be left out).
const sampleSchema = `
CREATE TABLE merchants (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	region     VARCHAR(10) NOT NULL DEFAULT 'us',
	active     BOOLEAN NOT NULL DEFAULT 1,
	balance    DECIMAL(10,2),
	rating     REAL,
	settings   JSON,
	logo       BLOB,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	opened_on  DATE,
	external   UUID,
	misc,
	slug       TEXT GENERATED ALWAYS AS (lower(name)) STORED,
	label      TEXT GENERATED ALWAYS AS (name || ' (' || region || ')') VIRTUAL
);
CREATE TABLE products (
	id          INTEGER PRIMARY KEY,
	merchant_id INTEGER NOT NULL REFERENCES merchants,
	name        TEXT NOT NULL
);
CREATE TABLE product_tags (
	product_id INTEGER NOT NULL,
	tag        TEXT NOT NULL,
	PRIMARY KEY (product_id, tag),
	FOREIGN KEY (product_id) REFERENCES products (id)
) WITHOUT ROWID;
CREATE TABLE accounts (
	id    INTEGER PRIMARY KEY,
	email TEXT NOT NULL UNIQUE,
	org   TEXT NOT NULL,
	slug  TEXT NOT NULL,
	UNIQUE (org, slug)
);
CREATE UNIQUE INDEX accounts_live ON accounts (slug) WHERE org != 'archived';
CREATE UNIQUE INDEX accounts_lower ON accounts (lower(email));
CREATE VIEW merchant_names AS SELECT name FROM merchants;
`

func TestPullSchemaMatchesGolden(t *testing.T) {
	p := open(t, filepath.Join(t.TempDir(), "shop.db"))
	exec(t, p, sampleSchema)

	capture, err := p.PullSchema(context.Background(), provider.PullOptions{})
	if err != nil {
		t.Fatalf("PullSchema: %v", err)
	}
	if capture.Kind != dbschema.SnapshotKind || capture.Target != "sqlite/shop.db" {
		t.Fatalf("kind/target: %q %q", capture.Kind, capture.Target)
	}

	var pretty strings.Builder
	var s dbschema.Schema
	if err := json.Unmarshal(capture.Body, &s); err != nil {
		t.Fatalf("body is not a dbschema.Schema: %v", err)
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	pretty.Write(b)
	pretty.WriteByte('\n')

	golden := filepath.Join("testdata", "schema.golden.json")
	if *updateGolden {
		if err := os.WriteFile(golden, []byte(pretty.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if pretty.String() != string(want) {
		t.Fatalf("schema differs from %s (run with -update and review the diff):\n%s", golden, pretty.String())
	}

	if _, ok := s.Tables["merchant_names"]; ok {
		t.Fatal("views must not be captured")
	}
}

func TestKindOf(t *testing.T) {
	cases := map[string]dbschema.Kind{
		"INTEGER": dbschema.KindInteger, "bigint": dbschema.KindInteger, "TINYINT(1)": dbschema.KindInteger,
		"TEXT": dbschema.KindText, "VARCHAR(10)": dbschema.KindText, "nchar(5)": dbschema.KindText, "CLOB": dbschema.KindText,
		"BLOB": dbschema.KindBinary, "": dbschema.KindOther,
		"REAL": dbschema.KindFloat, "DOUBLE PRECISION": dbschema.KindFloat, "FLOAT": dbschema.KindFloat,
		"DECIMAL(10,2)": dbschema.KindDecimal, "NUMERIC": dbschema.KindDecimal,
		"BOOLEAN": dbschema.KindBoolean, "DATE": dbschema.KindDate, "TIME": dbschema.KindTime,
		"DATETIME": dbschema.KindTimestamp, "TIMESTAMP": dbschema.KindTimestamp,
		"JSON": dbschema.KindJSON, "UUID": dbschema.KindUUID, "MONEY": dbschema.KindOther,
	}
	for in, want := range cases {
		if got := kindOf(in); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", in, got, want)
		}
	}
}

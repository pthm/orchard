package mysql

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/dbschema"
)

// These tests cover what needs no server: schema, config and DSN
// handling, and dispatch. Queries against a real MySQL run in
// test/integration.

func TestSchemaLoadsFromEmbeddedHCL(t *testing.T) {
	s := New().Schema()
	if s.Name != "mysql" {
		t.Fatalf("name: got %q want mysql", s.Name)
	}
	if s.Config["dsn"].Type != "string" || !s.Config["dsn"].Required {
		t.Fatalf("config.dsn: %+v", s.Config["dsn"])
	}
	if s.Actions["mysql_query"].Attrs["query"].Type != "string" {
		t.Fatalf("action.mysql_query.query: %+v", s.Actions["mysql_query"].Attrs["query"])
	}
}

func TestConfigureValidatesDSN(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]cty.Value
		want   string
	}{
		{"missing", nil, "dsn is required"},
		{"null", map[string]cty.Value{"dsn": cty.NullVal(cty.String)}, "dsn is required"},
		{"wrong type", map[string]cty.Value{"dsn": cty.NumberIntVal(5)}, "must be a string"},
		{"malformed driver dsn", map[string]cty.Value{"dsn": cty.StringVal("not a dsn")}, "invalid dsn"},
		{"url without host", map[string]cty.Value{"dsn": cty.StringVal("mysql:///db")}, "has no host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := New().Configure(context.Background(), c.config)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected %q error, got %v", c.want, err)
			}
		})
	}
}

// url.Parse errors quote the input; ours must not, or a typo in a
// DSN would print its password.
func TestConfigureURLErrorHidesPassword(t *testing.T) {
	err := New().Configure(context.Background(), map[string]cty.Value{
		"dsn": cty.StringVal("mysql://root:hunter2@[::1/db"),
	})
	if err == nil {
		t.Fatal("expected an error for a malformed URL")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

func TestParseDSN(t *testing.T) {
	cases := []struct {
		name                  string
		in                    string
		user, pass, addr, db  string
		wantParam, wantParamV string
	}{
		{
			name: "driver format",
			in:   "orchard:secret@tcp(db.internal:3307)/app",
			user: "orchard", pass: "secret", addr: "db.internal:3307", db: "app",
		},
		{
			name: "url",
			in:   "mysql://orchard:secret@db.internal:3307/app",
			user: "orchard", pass: "secret", addr: "db.internal:3307", db: "app",
		},
		{
			name: "url default port",
			in:   "mysql://orchard@localhost/app",
			user: "orchard", addr: "localhost:3306", db: "app",
		},
		{
			name: "url-encoded password",
			in:   "mysql://orchard:p%40ss%2Fw%3Ard@localhost/app",
			user: "orchard", pass: "p@ss/w:rd", addr: "localhost:3306", db: "app",
		},
		{
			name: "url query params pass through",
			in:   "mysql://orchard@localhost/app?autocommit=1",
			user: "orchard", addr: "localhost:3306", db: "app",
			wantParam: "autocommit", wantParamV: "1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseDSN(c.in)
			if err != nil {
				t.Fatalf("parseDSN: %v", err)
			}
			if cfg.User != c.user || cfg.Passwd != c.pass || cfg.Addr != c.addr || cfg.DBName != c.db {
				t.Fatalf("got user=%q pass=%q addr=%q db=%q", cfg.User, cfg.Passwd, cfg.Addr, cfg.DBName)
			}
			if !cfg.ParseTime || !cfg.MultiStatements {
				t.Fatalf("parseTime=%v multiStatements=%v, want both on", cfg.ParseTime, cfg.MultiStatements)
			}
			if c.wantParam != "" && cfg.Params[c.wantParam] != c.wantParamV {
				t.Fatalf("param %s: got %q want %q (params %v)", c.wantParam, cfg.Params[c.wantParam], c.wantParamV, cfg.Params)
			}
		})
	}
}

func TestExecuteBeforeConfigure(t *testing.T) {
	_, err := New().Execute(context.Background(), "mysql_query", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured error, got %v", err)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	// Configure doesn't dial, so a well-formed DSN is enough to reach
	// the dispatch switch.
	p := New()
	if err := p.Configure(context.Background(), map[string]cty.Value{
		"dsn": cty.StringVal("mysql://orchard@localhost/app"),
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	defer func() { _ = p.Close() }()

	_, err := p.Execute(context.Background(), "imaginary", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), `unknown action "imaginary"`) {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	p := New()
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestEnumValues(t *testing.T) {
	cases := map[string][]string{
		"enum('us','eu','apac')": {"us", "eu", "apac"},
		"enum('it''s','a,b')":    {"it's", "a,b"},
		"enum('')":               {""},
		"varchar(10)":            nil,
	}
	for in, want := range cases {
		got := enumValues(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("enumValues(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		dataType, columnType string
		want                 dbschema.Kind
	}{
		{"tinyint", "tinyint(1)", dbschema.KindBoolean},
		{"tinyint", "tinyint unsigned", dbschema.KindInteger},
		{"int", "int unsigned", dbschema.KindInteger},
		{"bigint", "bigint", dbschema.KindInteger},
		{"decimal", "decimal(10,2)", dbschema.KindDecimal},
		{"double", "double", dbschema.KindFloat},
		{"varchar", "varchar(100)", dbschema.KindText},
		{"enum", "enum('a')", dbschema.KindEnum},
		{"datetime", "datetime", dbschema.KindTimestamp},
		{"json", "json", dbschema.KindJSON},
		{"blob", "blob", dbschema.KindBinary},
		{"set", "set('a','b')", dbschema.KindOther},
	}
	for _, c := range cases {
		if got := kindOf(c.dataType, c.columnType); got != c.want {
			t.Errorf("kindOf(%q, %q) = %q, want %q", c.dataType, c.columnType, got, c.want)
		}
	}
}

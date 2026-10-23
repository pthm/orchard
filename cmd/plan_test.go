package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanChecksTypedActionsFromSnapshot: with a snapshot pulled, plan
// checks typed actions, and references to their outputs, without the
// database: the database file is deleted before planning.
func TestPlanChecksTypedActionsFromSnapshot(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".orchard"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(project, "shop.db")
	createDB(t, db, `
		CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL, region TEXT NOT NULL DEFAULT 'us');
		CREATE TABLE products (id INTEGER PRIMARY KEY, merchant_id INTEGER NOT NULL REFERENCES merchants(id), name TEXT NOT NULL);`)

	scenario := func(name, body string) string {
		path := filepath.Join(project, name+".hcl")
		writeFile(t, path, `scenario "`+name+`" {
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  provider "db" {
    path = "./shop.db"
  }
`+body+`
}
`)
		return path
	}
	good := scenario("good", `
  action "sqlite_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }
  action "sqlite_insert" "product" {
    table  = "products"
    values = { merchant_id = action.sqlite_insert.merchant.id, name = "Widget" }
  }
  output "region" { value = action.sqlite_insert.merchant.region }`)

	if out, err := runCLI(t, "schema", "pull", good); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if err := os.Remove(db); err != nil {
		t.Fatal(err)
	}

	if out, err := runCLI(t, "plan", good, "--no-color"); err != nil || strings.Contains(out, "note:") {
		t.Fatalf("a valid scenario should plan cleanly: %v\n%s", err, out)
	}

	cases := []struct{ name, body, want string }{
		{"missing_column", `
  action "sqlite_insert" "product" {
    table  = "products"
    values = { name = "Widget" }
  }`, `missing required column "merchant_id"`},
		{"table_typo", `
  action "sqlite_insert" "merchant" {
    table  = "merchant"
    values = { name = "Acme" }
  }`, `no such table "merchant" (did you mean "merchants"?) in the schema snapshot`},
		{"output_typo", `
  action "sqlite_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }
  action "sqlite_insert" "product" {
    table  = "products"
    values = { merchant_id = action.sqlite_insert.merchant.idd, name = "Widget" }
  }`, `attribute named "idd"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runCLI(t, "plan", scenario(c.name, c.body), "--no-color")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected %q, got %v\n%s", c.want, err, out)
			}
		})
	}

	t.Run("inside a component", func(t *testing.T) {
		writeFile(t, filepath.Join(project, "merchant.hcl"), `component "merchant" {
  variable "name" { type = string }
  action "sqlite_insert" "row" {
    table  = "merchants"
    values = { name = var.name }
  }
  output "id" { value = action.sqlite_insert.row.idd }
}
`)
		path := scenario("component_typo", `
  component "merchant" "acme" {
    source = "./merchant.hcl"
    inputs = { name = "Acme" }
  }`)
		out, err := runCLI(t, "plan", path, "--no-color")
		if err == nil || !strings.Contains(err.Error(), "idd") {
			t.Fatalf("expected a typo inside the component to fail, got %v\n%s", err, out)
		}
	})

	t.Run("without a snapshot", func(t *testing.T) {
		if err := os.Remove(filepath.Join(project, ".orchard", "schema", "db.json")); err != nil {
			t.Fatal(err)
		}
		out, err := runCLI(t, "plan", good, "--no-color")
		if err != nil {
			t.Fatalf("plan without a snapshot should still pass: %v\n%s", err, out)
		}
		if !strings.Contains(out, `note: no schema snapshot for provider "db"`) {
			t.Fatalf("expected a note about the missing snapshot:\n%s", out)
		}
	})
}

func TestPlanForeignKeyHint(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".orchard"), 0o755); err != nil {
		t.Fatal(err)
	}
	createDB(t, filepath.Join(project, "shop.db"), `
		CREATE TABLE merchants (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE products (id INTEGER PRIMARY KEY, merchant_id INTEGER NOT NULL REFERENCES merchants(id), name TEXT NOT NULL);`)
	path := filepath.Join(project, "shop.hcl")
	writeFile(t, path, `scenario "shop" {
  required_providers {
    db = { source = "builtin/sqlite" }
  }
  provider "db" {
    path = "./shop.db"
  }
  action "sqlite_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }
  action "sqlite_insert" "product" {
    depends_on = [action.sqlite_insert.merchant]
    table      = "products"
    values     = { merchant_id = 1, name = "Widget" }
  }
}
`)
	if out, err := runCLI(t, "schema", "pull", path); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	out, err := runCLI(t, "plan", path, "--no-color")
	if err != nil {
		t.Fatalf("a hint mustn't fail the plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hint: action.sqlite_insert.product (") || !strings.Contains(out, "shop.hcl:12)") ||
		!strings.Contains(out, "merchant_id is set to a fixed value (1), but it references merchants.id") {
		t.Fatalf("expected a foreign-key hint:\n%s", out)
	}
}

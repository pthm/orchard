package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [directory]",
		Short: "Scaffold a new Orchard project",
		Long: `Create a starter project layout with a hello-world scenario
and an example component. If no directory is given, the current
directory is used.

The scaffolded files are a starting point — edit them, rename them,
or delete what you don't need. Orchard has no required directory
structure; the layout is a convention, not a constraint.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			out := cmd.OutOrStdout()

			created := 0
			for _, f := range scaffoldFiles {
				path := filepath.Join(dir, f.path)
				if _, err := os.Stat(path); err == nil {
					fmt.Fprintf(out, "  skip %s (already exists)\n", f.path)
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return fmt.Errorf("creating directory for %s: %w", f.path, err)
				}
				if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
					return fmt.Errorf("writing %s: %w", f.path, err)
				}
				fmt.Fprintf(out, "  create %s\n", f.path)
				created++
			}

			if created == 0 {
				fmt.Fprintln(out, "\nNothing to do — all files already exist.")
			} else {
				fmt.Fprintf(out, "\nScaffolded %d file(s). Next steps:\n", created)
				fmt.Fprintln(out, "  1. Edit scenarios/hello.hcl with your database DSN")
				fmt.Fprintln(out, "  2. Run: orchard plan scenarios/hello.hcl")
				fmt.Fprintln(out, "  3. Run: orchard run scenarios/hello.hcl")
			}
			return nil
		},
	}
}

type scaffoldFile struct {
	path    string
	content string
}

var scaffoldFiles = []scaffoldFile{
	{
		path: "scenarios/hello.hcl",
		content: `# A starter scenario that inserts a merchant row and captures its ID.
# Edit the DSN variable default or pass it via --var:
#
#   orchard run scenarios/hello.hcl --var dsn="postgres://localhost/mydb"

scenario "hello" {
  description = "Insert a merchant and verify the pipeline works end-to-end"
  tags        = ["starter"]

  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {
    description = "PostgreSQL connection string"
  }

  variable "merchant_name" {
    description = "Name for the seed merchant"
    default     = "Acme Corp"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "seed" {
    source = "../components/merchant.hcl"
    inputs = {
      name = var.merchant_name
    }
  }

  output "merchant_id"   { value = component.merchant.seed.id }
  output "merchant_name" { value = component.merchant.seed.name }
}
`,
	},
	{
		path: "components/merchant.hcl",
		content: `# Reusable component: insert a merchant row.
# Scenarios instantiate this with:
#
#   component "merchant" "seed" {
#     source = "../components/merchant.hcl"
#     inputs = { name = "Acme Corp" }
#   }

component "merchant" {
  description = "Insert a merchant and expose its id and name"

  variable "name" {
    type = string
  }

  variable "region" {
    type    = string
    default = "us"
  }

  action "postgres_query" "create" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.name}', '${var.region}') RETURNING id, name, region"

    lifecycle {
      teardown {
        query = "DELETE FROM merchants WHERE id = ${self.id}"
      }
    }
  }

  output "id"     { value = action.postgres_query.create.id }
  output "name"   { value = action.postgres_query.create.name }
  output "region" { value = action.postgres_query.create.region }
}
`,
	},
}

func init() {
	// Registered in init rather than NewRootCmd so the file stays
	// self-contained — adding or removing the init command is a
	// one-file change.
}

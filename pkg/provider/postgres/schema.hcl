provider "postgres" {
  version  = "1"
  protocol = "1"

  description = "PostgreSQL data operations."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - Orchard does **not** manage schema. Tables must exist before the scenario runs.
    - Each `sql` action runs in its own transaction. There is no cross-action
      transactional scope today — if you need atomicity across multiple statements,
      bundle them into one `query` with a `BEGIN;...COMMIT;` block, or use a CTE.
    - SQL injection: values interpolated via `$${var.name}` are HCL-evaluated to
      strings and substituted as text. Use scenario variables for known-safe values;
      never pass untrusted input through the `query` attribute.
    - The DSN is passed to `pgxpool`. Connection opening is lazy — a syntactically
      valid DSN configures the provider successfully; the first action call is where
      connection errors surface.
  MD

  config {
    attribute "dsn" {
      type        = string
      required    = true
      description = "PostgreSQL connection string (e.g. `postgres://user:pass@host:5432/db`)."
    }
  }

  action "sql" {
    description = <<-MD
      Execute a SQL statement. The first row returned (typically from a `RETURNING`
      clause) is exposed as outputs keyed by column name.
    MD

    attribute "query" {
      type        = string
      required    = true
      description = "The SQL statement to execute."
    }

    output {
      type        = any
      description = <<-MD
        The first row of the result set, keyed by column name. Null if no rows
        were returned.

        Column types are mapped to cty as follows:

        | PostgreSQL type | cty type |
        |-----------------|----------|
        | `text`, `varchar`, `uuid`, `timestamp*`, `date` | `string` |
        | `int2`, `int4`, `int8`, `numeric`, `float4`, `float8` | `number` |
        | `bool` | `bool` |
        | `NULL` | `null` |

        If the query returns no rows (for example a pure `INSERT` without
        `RETURNING`), outputs are null.
      MD
    }

    example {
      description = "Insert a row and capture its generated ID with `RETURNING`."
      code        = <<-HCL
        action "sql" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us') RETURNING id, name"
        }

        action "sql" "create_product" {
          query = "INSERT INTO products (merchant_id, name) VALUES ($${action.sql.create_merchant.id}, 'Widget') RETURNING id, name"
        }
      HCL
    }
  }

  action "copy" {
    description = <<-MD
      Bulk-load rows into a table via PostgreSQL `COPY FROM STDIN`. Dramatically
      faster than row-by-row `INSERT` for fixture data — a 10,000-row CSV loads
      in a single network round-trip.

      Provide data inline via `data` or reference a file via `file`. The two are
      mutually exclusive. `file` paths are resolved relative to the scenario file
      by the engine, so `file = "./fixtures/merchants.csv"` works from any cwd.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Target table name."
    }
    attribute "columns" {
      type        = list(string)
      required    = true
      description = "Column names in the order the data provides them."
    }
    attribute "format" {
      type        = string
      default     = "csv"
      description = "Data format: csv, tsv, or text (PostgreSQL COPY formats)."
    }
    attribute "header" {
      type        = bool
      default     = false
      description = "When true, skip the first row as a header line."
    }
    attribute "data" {
      type        = string
      description = "Inline data string. Mutually exclusive with file."
    }
    attribute "file" {
      type        = string
      description = "Path to a data file. Resolved relative to the scenario file by the engine. Mutually exclusive with data."
    }
    attribute "delimiter" {
      type        = string
      description = "Field delimiter override. Defaults to comma for csv, tab for tsv."
    }

    output {
      type = object({ rows_copied = number })
      description = "Number of rows successfully loaded."
    }

    example {
      description = "Inline CSV for a handful of seed rows."
      code = <<-HCL
        action "copy" "seed_merchants" {
          table   = "merchants"
          columns = ["name", "region"]
          format  = "csv"
          data    = <<-CSV
            Acme Corp,us
            Globex,eu
          CSV
        }
      HCL
    }

    example {
      description = "Load a larger fixture from a checked-in file."
      code = <<-HCL
        action "copy" "seed_products" {
          table   = "products"
          columns = ["merchant_id", "name", "price"]
          format  = "csv"
          header  = true
          file    = "./fixtures/products.csv"
        }
      HCL
    }
  }

  example {
    description = "Seed a merchant and product, wiring the merchant's generated ID into the product insert."
    code        = <<-HCL
      scenario "seed_demo" {
        required_providers {
          postgres = { source = "builtin/postgres" }
        }

        variable "dsn" {}

        provider "postgres" {
          dsn = var.dsn
        }

        action "sql" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme Corp', 'us') RETURNING id, name"
        }

        action "sql" "create_product" {
          query = "INSERT INTO products (merchant_id, name, price) VALUES ($${action.sql.create_merchant.id}, 'Widget', 9.99) RETURNING id"
        }

        output "merchant_id" { value = action.sql.create_merchant.id }
        output "product_id"  { value = action.sql.create_product.id }
      }
    HCL
  }
}

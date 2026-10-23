provider "postgres" {
  version  = "1"
  protocol = "1"

  description = "PostgreSQL data operations."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - Orchard does **not** manage schema. Tables must exist before the scenario runs.
    - `postgres_insert`, `postgres_update` and `postgres_delete` are typed alternatives to `postgres_query`:
      values are bound as parameters, and each action is checked against the live
      schema before the run starts. A table that doesn't exist yet (because an
      earlier action creates it) is checked when the action runs instead.
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

  action "postgres_query" {
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
        action "postgres_query" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us') RETURNING id, name"
        }

        action "postgres_query" "create_product" {
          query = "INSERT INTO products (merchant_id, name) VALUES ($${action.postgres_query.create_merchant.id}, 'Widget') RETURNING id, name"
        }
      HCL
    }
  }

  action "postgres_copy" {
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
        action "postgres_copy" "seed_merchants" {
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
        action "postgres_copy" "seed_products" {
          table   = "products"
          columns = ["merchant_id", "name", "price"]
          format  = "csv"
          header  = true
          file    = "./fixtures/products.csv"
        }
      HCL
    }
  }

  action "postgres_insert" {
    description = <<-MD
      Insert one row. Values are sent as bind parameters, never pasted into SQL, and
      are checked against the table's schema before the run starts: the table and
      columns exist, every value fits its column, every required column (not
      nullable, no default, not generated) is set, and no generated column is.
      The outputs are the inserted row, including generated ids and defaults.

      `orchard teardown` deletes the row again by its primary key, unless the action
      has its own `lifecycle { teardown { } }` block or sets
      `lifecycle { managed_teardown = false }`.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Table name, or `schema.name`. Unqualified names resolve against the connection's current schema."
    }

    attribute "values" {
      type        = any
      required    = true
      description = "Object of `column = value`."
    }

    output {
      type        = any
      description = "The inserted row, keyed by column name. Columns come back typed by their kind: numbers, bools, strings, decoded JSON."
    }

    example {
      description = "Insert a merchant, then a product that references it."
      code        = <<-HCL
        action "postgres_insert" "merchant" {
          table  = "merchants"
          values = { name = "Acme", region = "us" }
        }

        action "postgres_insert" "product" {
          table  = "products"
          values = {
            merchant_id = action.postgres_insert.merchant.id
            name        = "Widget"
            price       = 9.99
          }
        }
      HCL
    }
  }

  action "postgres_update" {
    description = <<-MD
      Update the rows matching `where`, with the same checks and bind parameters as
      `postgres_insert`. Generated columns can't be set.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Table name, or `schema.name`. Unqualified names resolve against the connection's current schema."
    }

    attribute "set" {
      type        = any
      required    = true
      description = "Object of `column = new value`."
    }

    attribute "where" {
      type        = any
      required    = true
      description = "Object of `column = value` conditions, joined with `AND`; `null` matches `NULL`. Must name at least one column, so an update can't hit every row by accident. Use `postgres_query` for anything more complex."
    }

    output {
      type        = object({ rows_affected = number })
      description = "`rows_affected`: how many rows the update changed."
    }

    example {
      description = "Suspend the merchant inserted earlier."
      code        = <<-HCL
        action "postgres_update" "suspend" {
          table = "merchants"
          set   = { status = "suspended" }
          where = { id = action.postgres_insert.merchant.id }
        }
      HCL
    }
  }

  action "postgres_delete" {
    description = <<-MD
      Delete the rows matching `where`, checked against the table's schema before the
      run starts.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Table name, or `schema.name`. Unqualified names resolve against the connection's current schema."
    }

    attribute "where" {
      type        = any
      required    = true
      description = "Object of `column = value` conditions, joined with `AND`; `null` matches `NULL`. Must name at least one column."
    }

    output {
      type        = object({ rows_affected = number })
      description = "`rows_affected`: how many rows were deleted."
    }

    example {
      description = "Remove a merchant's test products."
      code        = <<-HCL
        action "postgres_delete" "test_products" {
          table = "products"
          where = { merchant_id = action.postgres_insert.merchant.id, name = "Test" }
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

        action "postgres_query" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme Corp', 'us') RETURNING id, name"
        }

        action "postgres_query" "create_product" {
          query = "INSERT INTO products (merchant_id, name, price) VALUES ($${action.postgres_query.create_merchant.id}, 'Widget', 9.99) RETURNING id"
        }

        output "merchant_id" { value = action.postgres_query.create_merchant.id }
        output "product_id"  { value = action.postgres_query.create_product.id }
      }
    HCL
  }
}

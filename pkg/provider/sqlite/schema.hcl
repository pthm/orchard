provider "sqlite" {
  version  = "1"
  protocol = "1"

  description = "SQLite data operations."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - Orchard does **not** manage schema. Tables must exist before the scenario runs,
      or create them with a `CREATE TABLE IF NOT EXISTS` action that the rest of the
      scenario depends on.
    - Foreign keys are **enforced**. SQLite leaves them off by default; Orchard turns
      them on for every connection so scenarios fail loudly on a dangling reference,
      the same as on Postgres.
    - Each `sqlite_query` runs on its own, outside any transaction spanning actions.
      If you need several statements to apply atomically, wrap them in one `query`
      with `BEGIN; ... COMMIT;`.
    - Actions run one at a time over a single connection. That keeps a `:memory:`
      database shared by every action in the run.
    - A `:memory:` database disappears when the run ends, so `orchard teardown` has
      nothing to clean up. Use a file path for state you want to tear down later.
    - SQL injection: values interpolated via `$${var.name}` are HCL-evaluated to
      strings and substituted as text. Use scenario variables for known-safe values;
      never pass untrusted input through the `query` attribute.
  MD

  config {
    attribute "path" {
      type        = string
      required    = true
      path        = true
      description = <<-MD
        Path to the database file. A relative path is relative to the scenario file, so
        the scenario works from any directory. The file is created if it doesn't exist.
        Use `:memory:` for a database that lasts only for the run.
      MD
    }
  }

  action "sqlite_query" {
    description = <<-MD
      Execute a SQL statement. If it returns rows (a `SELECT`, or a write with
      `RETURNING`), the first row is exposed as outputs keyed by column name. If it
      returns no rows, the outputs are `last_insert_id` and `rows_affected`.
    MD

    attribute "query" {
      type        = string
      required    = true
      description = "The SQL statement to execute."
    }

    output {
      type        = any
      description = <<-MD
        For a statement that returns rows: the first row of the result set, keyed by
        column name, or empty if no rows matched.

        For a statement that returns no rows (`INSERT`, `UPDATE` or `DELETE` without
        `RETURNING`, or DDL):

        | Output | Description |
        |--------|-------------|
        | `last_insert_id` | Rowid of the most recent successful `INSERT` on the connection. Only meaningful after an `INSERT`. |
        | `rows_affected` | Rows changed by the statement. |

        Column values are mapped to cty as follows:

        | SQLite value | cty type |
        |--------------|----------|
        | `TEXT`, `BLOB` | `string` |
        | `INTEGER`, `REAL` | `number` |
        | `DATE`, `DATETIME`, `TIMESTAMP` columns | `string` (RFC 3339) |
        | `NULL` | `null` |
      MD
    }

    example {
      description = "Insert a row and capture its generated ID with `RETURNING`."
      code        = <<-HCL
        action "sqlite_query" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us') RETURNING id, name"
        }

        action "sqlite_query" "create_product" {
          query = "INSERT INTO products (merchant_id, name) VALUES ($${action.sqlite_query.create_merchant.id}, 'Widget') RETURNING id"
        }
      HCL
    }

    example {
      description = "Without `RETURNING`, wire `last_insert_id` into the next statement."
      code        = <<-HCL
        action "sqlite_query" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')"
        }

        action "sqlite_query" "create_product" {
          query = "INSERT INTO products (merchant_id, name) VALUES ($${action.sqlite_query.create_merchant.last_insert_id}, 'Widget')"
        }
      HCL
    }
  }

  example {
    description = "A complete scenario against a local database file, with teardown."
    code        = <<-HCL
      scenario "sqlite_demo" {
        required_providers {
          sqlite = { source = "builtin/sqlite" }
        }

        provider "sqlite" {
          path = "./dev.db"
        }

        action "sqlite_query" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us') RETURNING id"

          lifecycle {
            teardown {
              query = "DELETE FROM merchants WHERE id = $${self.id}"
            }
          }
        }

        output "merchant_id" { value = action.sqlite_query.create_merchant.id }
      }
    HCL
  }
}

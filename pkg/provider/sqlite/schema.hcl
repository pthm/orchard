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
    - `sqlite_insert`, `sqlite_update` and `sqlite_delete` are typed alternatives to `sqlite_query`:
      values are bound as parameters, and each action is checked against the live
      schema before the run starts. A table that doesn't exist yet (because an
      earlier action creates it) is checked when the action runs instead.
    - Foreign keys are **enforced**. SQLite leaves them off by default; Orchard turns
      them on for every connection so scenarios fail loudly on a dangling reference,
      the same as on Postgres.
    - Each `sqlite_query` runs on its own, outside any transaction spanning actions.
      If you need several statements to apply atomically, wrap them in one `query`
      with `BEGIN; ... COMMIT;`.
    - Actions run one at a time over a single connection, even with
      `orchard run --parallelism`. That keeps a `:memory:` database shared by
      every action in the run.
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

    attribute "params" {
      type        = any
      description = "Values for the query's `?` (or `?1`, `?2`, ...) placeholders, in order. They're sent separately from the SQL, so they need no quoting or escaping, and null binds as NULL. A list or object goes as JSON text, for `json_each(?)`."
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

    example {
      description = "Bind values to `?` placeholders with `params` instead of interpolating them into the SQL."
      code        = <<-HCL
        action "sqlite_query" "merchant" {
          query  = "INSERT INTO merchants (name, region) VALUES (?, ?) RETURNING id"
          params = [var.merchant_name, "eu"]
        }
      HCL
    }
  }

  action "sqlite_insert" {
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
      description = "Table name."
    }

    attribute "values" {
      type        = any
      required    = true
      description = "Object of `column = value`."
    }

    attribute "on_conflict" {
      type        = string
      default     = "error"
      description = <<-MD
        What to do when a row with the same conflict columns already exists:
        `"error"` (the database rejects the insert), `"ignore"` (leave the row as it
        is), or `"update"` (update it with `values`). With `"ignore"` or `"update"`
        the outputs are the existing row, and `orchard teardown` doesn't delete it;
        an update of it is restored like a typed update.
      MD
    }

    attribute "conflict_columns" {
      type        = list(string)
      description = "Columns that identify an existing row for `on_conflict`: the primary key (the default) or a unique key. `values` must set them."
    }

    output {
      type        = any
      description = "The inserted row, keyed by column name. Columns come back typed by their kind: numbers, bools, strings, decoded JSON."
    }

    example {
      description = "Insert a merchant, then a product that references it."
      code        = <<-HCL
        action "sqlite_insert" "merchant" {
          table  = "merchants"
          values = { name = "Acme", region = "us" }
        }

        action "sqlite_insert" "product" {
          table  = "products"
          values = {
            merchant_id = action.sqlite_insert.merchant.id
            name        = "Widget"
            price       = 9.99
          }
        }
      HCL
    }
  }

  action "sqlite_update" {
    description = <<-MD
      Update the rows matching `where`, with the same checks and bind parameters as
      `sqlite_insert`. Generated columns can't be set.

      `orchard teardown` writes back the values the update overwrote, for rows
      inserted earlier in the same run. Set `lifecycle { managed_teardown = true }`
      to restore other rows too, or `false` to restore nothing. Updates matching more
      than 1,000 rows aren't restored.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Table name."
    }

    attribute "set" {
      type        = any
      required    = true
      description = "Object of `column = new value`."
    }

    attribute "where" {
      type        = any
      required    = true
      description = "Object of `column = value` conditions, joined with `AND`; `null` matches `NULL`. Must name at least one column, so an update can't hit every row by accident. Use `sqlite_query` for anything more complex."
    }

    output {
      type        = object({ rows_affected = number })
      description = "`rows_affected`: how many rows the update changed."
    }

    example {
      description = "Suspend the merchant inserted earlier."
      code        = <<-HCL
        action "sqlite_update" "suspend" {
          table = "merchants"
          set   = { status = "suspended" }
          where = { id = action.sqlite_insert.merchant.id }
        }
      HCL
    }
  }

  action "sqlite_delete" {
    description = <<-MD
      Delete the rows matching `where`, checked against the table's schema before the
      run starts.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Table name."
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
        action "sqlite_delete" "test_products" {
          table = "products"
          where = { merchant_id = action.sqlite_insert.merchant.id, name = "Test" }
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

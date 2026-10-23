provider "mysql" {
  version  = "1"
  protocol = "1"

  description = "MySQL and MariaDB data operations."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - Orchard does **not** manage schema. Tables must exist before the scenario runs.
    - `mysql_insert`, `mysql_update` and `mysql_delete` are typed alternatives to `mysql_query`:
      values are bound as parameters, and each action is checked against the live
      schema before the run starts. A table that doesn't exist yet (because an
      earlier action creates it) is checked when the action runs instead.
    - MySQL has no `RETURNING` clause. After an `INSERT`, wire the new row's id into
      later steps with `last_insert_id` (see the examples below). MariaDB 10.5+ does
      support `RETURNING`, and its rows come back as outputs like any `SELECT`.
    - Each `mysql_query` runs on its own, outside any transaction spanning actions.
      One `query` can hold several statements, so wrap them in
      `START TRANSACTION; ... COMMIT;` if they must apply atomically. With several
      statements, `last_insert_id` and `rows_affected` describe the last one.
    - SQL injection: values interpolated via `$${var.name}` are HCL-evaluated to
      strings and substituted as text. Use scenario variables for known-safe values;
      never pass untrusted input through the `query` attribute.
    - Connection opening is lazy — a well-formed DSN configures the provider
      successfully; the first action call is where connection errors surface.
  MD

  config {
    attribute "dsn" {
      type        = string
      required    = true
      description = <<-MD
        Connection string, either as a URL (`mysql://user:pass@host:3306/db`) or in the
        Go driver's format (`user:pass@tcp(host:3306)/db`). Query parameters are passed
        to the driver as options. `parseTime` and `multiStatements` are always on.
      MD
    }
  }

  action "mysql_query" {
    description = <<-MD
      Execute a SQL statement. If it returns rows (a `SELECT`), the first row is
      exposed as outputs keyed by column name. If it returns no rows, the outputs are
      `last_insert_id` and `rows_affected`.
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

        For a statement that returns no rows (`INSERT`, `UPDATE`, `DELETE`, or DDL):

        | Output | Description |
        |--------|-------------|
        | `last_insert_id` | `LAST_INSERT_ID()`: the first `AUTO_INCREMENT` id the most recent `INSERT` generated on the connection. `0` if there hasn't been one. |
        | `rows_affected` | `ROW_COUNT()`: rows the statement changed. An `UPDATE` that sets a column to its current value doesn't count. |

        Column values are mapped to cty as follows:

        | MySQL type | cty type |
        |------------|----------|
        | `TINYINT`…`BIGINT` (including `BOOL`), `DECIMAL`, `FLOAT`, `DOUBLE` | `number` |
        | `CHAR`, `VARCHAR`, `TEXT`, `ENUM`, `JSON`, `BLOB`, `TIME` | `string` |
        | `DATE`, `DATETIME`, `TIMESTAMP` | `string` (RFC 3339) |
        | `NULL` | `null` |
      MD
    }

    example {
      description = "Insert a row, then use its `AUTO_INCREMENT` id in the next statement."
      code        = <<-HCL
        action "mysql_query" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')"
        }

        action "mysql_query" "create_product" {
          query = "INSERT INTO products (merchant_id, name) VALUES ($${action.mysql_query.create_merchant.last_insert_id}, 'Widget')"
        }
      HCL
    }

    example {
      description = "Read a row back with `SELECT`; its columns become outputs."
      code        = <<-HCL
        action "mysql_query" "merchant" {
          query = "SELECT id, name FROM merchants WHERE id = $${action.mysql_query.create_merchant.last_insert_id}"
        }
      HCL
    }
  }

  action "mysql_insert" {
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
      description = "Table name in the connected database."
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
        action "mysql_insert" "merchant" {
          table  = "merchants"
          values = { name = "Acme", region = "us" }
        }

        action "mysql_insert" "product" {
          table  = "products"
          values = {
            merchant_id = action.mysql_insert.merchant.id
            name        = "Widget"
            price       = 9.99
          }
        }
      HCL
    }
  }

  action "mysql_update" {
    description = <<-MD
      Update the rows matching `where`, with the same checks and bind parameters as
      `mysql_insert`. Generated columns can't be set.

      `orchard teardown` writes back the values the update overwrote, for rows
      inserted earlier in the same run. Set `lifecycle { managed_teardown = true }`
      to restore other rows too, or `false` to restore nothing. Updates matching more
      than 1,000 rows aren't restored.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Table name in the connected database."
    }

    attribute "set" {
      type        = any
      required    = true
      description = "Object of `column = new value`."
    }

    attribute "where" {
      type        = any
      required    = true
      description = "Object of `column = value` conditions, joined with `AND`; `null` matches `NULL`. Must name at least one column, so an update can't hit every row by accident. Use `mysql_query` for anything more complex."
    }

    output {
      type        = object({ rows_affected = number })
      description = "`rows_affected`: how many rows the update changed."
    }

    example {
      description = "Suspend the merchant inserted earlier."
      code        = <<-HCL
        action "mysql_update" "suspend" {
          table = "merchants"
          set   = { status = "suspended" }
          where = { id = action.mysql_insert.merchant.id }
        }
      HCL
    }
  }

  action "mysql_delete" {
    description = <<-MD
      Delete the rows matching `where`, checked against the table's schema before the
      run starts.
    MD

    attribute "table" {
      type        = string
      required    = true
      description = "Table name in the connected database."
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
        action "mysql_delete" "test_products" {
          table = "products"
          where = { merchant_id = action.mysql_insert.merchant.id, name = "Test" }
        }
      HCL
    }
  }

  example {
    description = "A complete scenario, with teardown, reading the DSN from the environment."
    code        = <<-HCL
      scenario "mysql_demo" {
        required_providers {
          mysql = { source = "builtin/mysql" }
        }

        variable "dsn" {
          default = env("DATABASE_URL")
        }

        provider "mysql" {
          dsn = var.dsn
        }

        action "mysql_query" "create_merchant" {
          query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')"

          lifecycle {
            teardown {
              query = "DELETE FROM merchants WHERE id = $${self.last_insert_id}"
            }
          }
        }

        output "merchant_id" { value = action.mysql_query.create_merchant.last_insert_id }
      }
    HCL
  }
}

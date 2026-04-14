---
title: postgres
weight: 10
---

# postgres

PostgreSQL data operations.

**Source:** `builtin/postgres`

## Provider configuration

```hcl
provider "postgres" {
  dsn = "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
}
```

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `dsn` | `string` | yes | — | PostgreSQL connection string (e.g. `postgres://user:pass@host:5432/db`). |

The DSN is passed to `pgxpool`. Connection opening is lazy — a syntactically valid
DSN configures the provider successfully; the first action call is where
connection errors surface.

## Actions

### `sql`

Execute a SQL statement. The first row returned (typically from a `RETURNING`
clause) is exposed as outputs keyed by column name.

```hcl
action "sql" "create_merchant" {
  query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us') RETURNING id, name"
}
```

**Attributes**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `query` | `string` | yes | — | The SQL statement to execute. |

**Outputs**

Type: `any`.

The action's outputs are the first row of the result set, keyed by column name.
Column types are mapped to cty as follows:

| PostgreSQL type | cty type |
|-----------------|----------|
| `text`, `varchar`, `uuid`, `timestamp*`, `date` | `string` |
| `int2`, `int4`, `int8`, `numeric`, `float4`, `float8` | `number` |
| `bool` | `bool` |
| `NULL` | `null` |

If the query returns no rows (for example a pure `INSERT` without `RETURNING`),
outputs are null. To wire values into downstream actions, use `RETURNING`:

```hcl
action "sql" "create_product" {
  query = "INSERT INTO products (merchant_id, name) VALUES (${action.sql.create_merchant.id}, 'Widget') RETURNING id, name"
}
```

## Complete example

```hcl
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
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.sql.create_merchant.id}, 'Widget', 9.99) RETURNING id"
  }

  output "merchant_id" { value = action.sql.create_merchant.id }
  output "product_id"  { value = action.sql.create_product.id }
}
```

## Notes

- Orchard does **not** manage schema. Tables must exist before the scenario runs.
- Each `sql` action runs in its own transaction. There is no cross-action
  transactional scope today — if you need atomicity across multiple statements,
  bundle them into one `query` with a `BEGIN;...COMMIT;` block, or use a CTE.
- SQL injection: values interpolated via `${var.name}` are HCL-evaluated to
  strings and substituted as text. Use scenario variables for known-safe values;
  never pass untrusted input through the `query` attribute.

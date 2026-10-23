---
title: Typed database actions
weight: 60
---

**Goal.** Insert, update and delete rows without hand-writing SQL, with mistakes
caught before anything touches the database, and cleanup that writes itself.

**Why Orchard.** A raw `postgres_query` is a string: Orchard can't see a misspelt
column, a missing required value or a quote that breaks the statement until the
database rejects it, usually halfway through a scenario. Typed actions know the
table's shape, so Orchard checks them up front, binds values safely, and knows how
to undo what they did.

## The actions

Each SQL provider has three, alongside its raw `*_query` action:

| Action | Attributes | Outputs |
|---|---|---|
| `postgres_insert`, `mysql_insert`, `sqlite_insert` | `table`, `values` | The inserted row, every column |
| `postgres_update`, `mysql_update`, `sqlite_update` | `table`, `set`, `where` | `rows_affected` |
| `postgres_delete`, `mysql_delete`, `sqlite_delete` | `table`, `where` | `rows_affected` |

`values`, `set` and `where` are objects of `column = value`. `where` matches on
equality, joined with `AND` (`null` matches `NULL`), and must name at least one
column, so an update or delete can't hit every row by accident.

## Example

A component that creates a merchant and its first product:

```hcl
# components/merchant.hcl
component "merchant" {
  variable "name" { type = string }
  variable "region" {
    type    = string
    default = "us"
  }

  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = var.name, region = var.region }
  }

  action "postgres_insert" "product" {
    table  = "products"
    values = {
      merchant_id = action.postgres_insert.merchant.id
      name        = "Starter plan"
      price       = 9.99
    }
  }

  output "id"         { value = action.postgres_insert.merchant.id }
  output "created_at" { value = action.postgres_insert.merchant.created_at }
}
```

And a scenario that uses it, then suspends the merchant:

```hcl
# scenarios/suspended_merchant.hcl
scenario "suspended_merchant" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" { default = env("DATABASE_URL") }

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "acme" {
    source = "../components/merchant.hcl"
    inputs = { name = "O'Brien & Co" }
  }

  action "postgres_update" "suspend" {
    table = "merchants"
    set   = { status = "suspended" }
    where = { id = component.merchant.acme.id }
  }
}
```

A few things to notice:

- **No SQL, no quoting.** Values are sent as bind parameters, so `O'Brien & Co`
  goes in as written.
- **The insert's outputs are the whole row.** `id` and `created_at` come from the
  database (a sequence and a default) and are available to later steps.
- **No `lifecycle` blocks.** `orchard teardown` deletes both rows by their primary
  keys, product first (see [automatic teardown](#automatic-teardown)).

## Checked before anything runs

`orchard run` checks every typed action against the live database before it
executes any of them:

- the table exists, and every column in `values`, `set` and `where` does too;
- each value fits its column (`"5"` from a string variable fits an integer column;
  `"cheap"` doesn't), integers are whole, enum values are members;
- every required column (not nullable, no default, not generated) is set;
- no generated or `GENERATED ALWAYS` identity column is set;
- `null` only goes in nullable columns.

Every problem in an action is reported at once, with suggestions:

```
Error: validation failed: component.merchant.acme.action.postgres_insert.product (components/merchant.hcl:14): table "public.products":
  - values: no column "prise" (did you mean "price"?)
  - missing required column "price"
```

References to an insert's outputs are checked against the table's columns too, so
`action.postgres_insert.merchant.idd` fails here rather than mid-run.

If an earlier action creates a table (a `CREATE TABLE IF NOT EXISTS` in a
`*_query`), the table doesn't exist yet when the run starts. Orchard skips it
then, and checks the action fully when it runs.

## Checking in plan, without a database

`orchard plan` never connects to anything, so it checks typed actions against a
**schema snapshot** instead. Take one with
[`orchard schema pull`](/reference/cli/#orchard-schema-pull):

```sh
orchard schema pull scenarios/suspended_merchant.hcl
#   wrote .orchard/schema/postgres.json (postgres/app_dev)
```

That reads the database's catalog (tables, columns, keys, enums, comments; never
data or connection details) and writes one file per provider alias, in the
nearest `.orchard/` directory above the scenario. Then:

- **Commit the snapshot.** `orchard plan` in CI can then catch every check above
  with no database running, and schema changes show up in review.
- **Re-pull after migrations.** Pulling an unchanged schema leaves the file alone,
  so re-pulling is cheap and doesn't create noise.
- **A table the scenario creates itself** isn't in a snapshot pulled before the
  scenario first ran, so plan reports it missing. Run the scenario once and
  re-pull.

Without a snapshot, plan still passes and says what it skipped:

```
note: no schema snapshot for provider "postgres", so its typed actions weren't checked against the database; run "orchard schema pull scenarios/suspended_merchant.hcl" to check them here
```

## Automatic teardown

When a typed insert runs, its provider records how to undo it, a delete of the
new row by its primary key, in the run record. `orchard teardown` runs, for each
action, in reverse dependency order:

1. its explicit `lifecycle { teardown { } }` block, if it has one;
2. otherwise the recorded undo;
3. otherwise nothing.

To keep a row through teardown, for reference data that other runs share, say,
opt out:

```hcl
action "postgres_insert" "region" {
  table  = "regions"
  values = { code = "eu" }

  lifecycle {
    managed_teardown = false
  }
}
```

Tables without a primary key, updates and deletes have no automatic undo. Give
them an explicit `teardown` block if they need one. See
[lifecycle and teardown](../../concepts/lifecycle/).

## Values and types

Each column has a portable kind, worked out from its native type. Values convert
to it, and come back typed:

| Column kind | Accepts | Comes back as |
|---|---|---|
| integer, decimal, float | numbers, or strings that parse as numbers | number (decimals keep full precision) |
| boolean | `true` / `false` (MySQL `tinyint(1)` included) | bool |
| text, uuid, enum, date, time, timestamp | strings (numbers and bools convert) | string; timestamps as RFC 3339 |
| json | anything; objects and lists are JSON-encoded | the decoded value |
| binary | strings | string |
| anything else (arrays, intervals, geometry) | anything, unchecked | string |

## Differences between databases

- **MySQL** has no `RETURNING`, so after an insert Orchard reads the row back by
  its primary key, taking an `AUTO_INCREMENT` key from the insert. A table without
  a primary key returns the values you gave, plus `last_insert_id`.
- **SQLite** resolves a relative `path` against the scenario file, and enforces
  foreign keys.
- **Postgres** tables can be written `schema.name`; unqualified names resolve
  against the connection's current schema. `orchard schema pull --schemas a,b`
  narrows the snapshot when extensions add schemas full of internal tables.

## When to use `*_query` instead

Typed actions cover single-row inserts and equality-matched updates and deletes.
Reach for the raw action when you need:

- conditions beyond equality (`WHERE created_at < now() - interval '30 days'`);
- upserts, joins, CTEs or anything else SQL does that a single-table write doesn't;
- statements that span several tables in one transaction
  (`BEGIN; ... COMMIT;` in one `query`);
- bulk loading: `postgres_copy` for Postgres, or `count` / `for_each` over a typed
  insert for smaller sets.

Both kinds of action can sit in the same scenario, on the same provider, and wire
into each other.

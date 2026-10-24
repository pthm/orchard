---
title: Isolating runs
weight: 65
---

**Goal.** Clean up a whole run in one step, however many rows it created, and
keep concurrent runs (parallel CI jobs, two engineers on one database) from
touching each other's data.

**Why.** A teardown per action works when each action creates one thing you
can name: `DELETE ... WHERE id = ${self.id}`. It falls apart for bulk inserts,
`postgres_copy`, rows that triggers create, or anything else without a tidy
`RETURNING`. Put the run's data inside a boundary instead, and have teardown
remove the boundary.

Both patterns below rely on one fact: a variable's default is evaluated once
per run and written to the run record, and `orchard teardown` reuses the
recorded value. So `default = uuid()` gives each run its own name, and its
teardown sees that same name.

## Pattern: a schema per run (Postgres)

Every table the run uses lives in a schema created for that run. Teardown drops
the schema.

```hcl
scenario "isolated" {
  required_providers {
    pg = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  variable "schema" {
    default = "orchard_${substr(replace(uuid(), "-", ""), 0, 12)}"
  }

  # search_path sends unqualified table names to the run's schema. Use `?`
  # instead of `&` if your DSN has no query string.
  provider "pg" {
    dsn = "${var.dsn}&search_path=${var.schema}"
  }

  action "postgres_query" "schema" {
    query = <<-SQL
      CREATE SCHEMA ${var.schema};
      CREATE TABLE ${var.schema}.merchants (
        id   BIGSERIAL PRIMARY KEY,
        name TEXT NOT NULL
      )
    SQL

    lifecycle {
      teardown {
        query = "DROP SCHEMA ${var.schema} CASCADE"
      }
    }
  }

  action "postgres_insert" "acme" {
    depends_on = [action.postgres_query.schema]
    table      = "merchants"
    values     = { name = "Acme" }

    # Dropping the schema removes the row; no need to delete it first.
    lifecycle { managed_teardown = false }
  }

  action "postgres_query" "bulk" {
    depends_on = [action.postgres_query.schema]
    query      = "INSERT INTO merchants (name) SELECT 'm' || n FROM generate_series(1, 50) n"
  }

  output "schema" { value = var.schema }
}
```

- **Every action depends on the schema action**, so its teardown, which runs
  in reverse order, comes last.
- **The run has to create its tables.** The schema starts empty, so this
  pattern suits test suites that run their migrations (or a
  `CREATE TABLE ... (LIKE public.merchants INCLUDING ALL)`) inside the
  scenario. An application reading `public` won't see the data unless it is
  pointed at the run's schema too. The `schema` output tells your test
  harness which one to use.
- **Typed actions are checked when they run**, because their tables don't
  exist before the run, and a snapshot pulled from another schema doesn't
  describe them.
- To pick the schema yourself (to match a CI job, say), pass
  `--var schema=ci_1234`.

## Pattern: a tag per run

The run writes its tag into every row it creates. A single teardown deletes by
that tag. This works against a shared schema that an application is already
reading.

```hcl
scenario "tagged" {
  required_providers {
    pg = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  variable "run_tag" {
    default = substr(replace(uuid(), "-", ""), 0, 12)
  }

  provider "pg" {
    dsn = var.dsn
  }

  # Holds the cleanup for every row the run tags. It runs first, so its
  # teardown runs last, after any action that cleans up on its own.
  action "postgres_query" "sweep" {
    query = "SELECT '${var.run_tag}' AS run_tag"

    lifecycle {
      teardown {
        query = "DELETE FROM merchants WHERE name LIKE '%#${var.run_tag}'"
      }
    }
  }

  # Bulk insert, no per-row teardown. Products follow their merchant
  # through ON DELETE CASCADE.
  action "postgres_query" "merchants" {
    depends_on = [action.postgres_query.sweep]
    query      = <<-SQL
      WITH m AS (
        INSERT INTO merchants (name)
        SELECT 'merchant ' || n || '#${var.run_tag}' FROM generate_series(1, 20) n
        RETURNING id
      )
      INSERT INTO products (merchant_id, name, price)
      SELECT id, 'widget', 9.99 FROM m
    SQL
  }
}
```

- **Where the tag goes.** A dedicated column (`run_tag TEXT`, indexed) is the
  cleanest choice if you can add one. Otherwise, like the example, put it in a
  natural key: a name suffix or an email such as `buyer+${var.run_tag}@example.com`.
  The tag also works outside the database: S3 object metadata, Redis key
  prefixes, an HTTP header your service logs. See
  [tagging a run's resources](../../concepts/lifecycle/#tagging-a-runs-resources).
- **Rows created through foreign keys** go with their parent when the
  constraint says `ON DELETE CASCADE`. Without it, give the sweep one `DELETE`
  per table, children first, in a multi-statement query (which runs as one
  transaction).
- **Components** can take the tag as an input
  (`inputs = { tag = var.run_tag }`). The record stores each component's
  variables, so a teardown body inside the component sees the same `var.tag`.
- **Lost records.** If the record file is gone, you can still clean up by hand
  with the tag: `DELETE ... WHERE name LIKE '%#<tag>'`. While the record exists,
  `orchard inspect <run-id>` shows the run's variables, tag included.

## Choosing

| | Schema per run | Tag per run |
|---|---|---|
| Teardown | One `DROP SCHEMA`; nothing left behind | Only rows that carry the tag |
| Parallel runs | Fully separate | Separate as long as every query filters by tag |
| Application sees the data | Only if pointed at the run's schema | Yes, it's in the usual tables |
| Needs | `CREATE` on the database; the run creates its tables | A place for the tag in each row |

For automated suites that own their database, use a schema per run. For data
an app or a person looks at (demos, UAT, a shared dev database), use a tag per
run.

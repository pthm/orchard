---
title: Lifecycle and teardown
weight: 60
---

Orchard creates state. Good tools also clean it up. Every `orchard run` writes
a **run record** describing what happened; `orchard teardown` uses that record
to reverse it.

## Run records

After every `orchard run`, Orchard writes a JSON file named
`orchard_<scenario>_run_<id>.json` into the current directory. The filename's
`<id>` prefix is sortable — `YYYYMMDDTHHMMSSZ_<8hex>` — so listing the
directory in your shell gives you runs in chronological order.

```
$ orchard run scenarios/dev.hcl
scenario: dev
executing...
  ok action.postgres_query.create_merchant (4ms)
  ok action.postgres_query.create_product (2ms)
...

$ ls orchard_dev_run_*.json
orchard_dev_run_20260415T103045Z_a3f29e5b.json
```

The record captures:

- The scenario name and the absolute path it ran from.
- The resolved scenario variables.
- Per-node timing and outputs.
- The scenario-level outputs.
- A top-level status: `succeeded` or `failed`.

Records land in `.` by default. Override with `--record-dir <path>` or skip
entirely with `--no-record`.

```sh
orchard run scenarios/dev.hcl --record-dir .orchard/runs
orchard run scenarios/ci.hcl  --no-record
```

## `lifecycle { teardown { } }` blocks

To make an action teardownable, attach a `lifecycle { teardown { } }` block
whose body declares the inverse operation in the same provider's schema:

```hcl
action "postgres_query" "create_merchant" {
  query = "INSERT INTO merchants (name) VALUES ('${var.name}') RETURNING id, name"

  lifecycle {
    teardown {
      query = "DELETE FROM merchants WHERE id = ${self.id}"
    }
  }
}
```

The `teardown` block is "another action body" for the same provider — a sql
action's teardown declares `query`; an http action's declares `method` + `url`;
an exec action's declares `command` + `args`.

Inside the teardown body, three reference namespaces are in scope:

- `var.*` — scenario variables.
- `self.*` — this action's own recorded outputs. Use it to target the specific
  resource you created.
- `action.*` — any other action's recorded outputs, for teardowns that need
  upstream state.

## Automatic teardown for typed actions

Typed database inserts and updates (`postgres_insert`, `mysql_update`, ...) don't
need a `lifecycle` block. When one runs, its provider records how to undo it in
the run record, and `orchard teardown` runs that: an insert's new row is deleted
by its primary key, and an update's overwritten values are written back.

```hcl
action "postgres_insert" "merchant" {
  table  = "merchants"
  values = { name = var.name }
}
# orchard teardown deletes the row: DELETE FROM merchants WHERE id = <its id>
```

Which teardown runs, for each action:

1. An explicit `lifecycle { teardown { } }` block, if the action has one.
2. Otherwise the automatic undo, if its provider recorded one.
3. Otherwise nothing.

An update restores rows inserted earlier in the same run by default, and other
rows only with `managed_teardown = true`, since rewriting a row Orchard didn't
create could undo someone else's change. To keep a typed insert's row through
teardown, opt out:

```hcl
action "postgres_insert" "reference_data" {
  table  = "regions"
  values = { code = "eu" }

  lifecycle {
    managed_teardown = false
  }
}
```

`managed_teardown = false` also stops an update from restoring anything. Tables
without a primary key get no automatic teardown, since there's no way to target
just the affected rows, and neither do deletes or updates matching more than
1,000 rows; give them an explicit `teardown` block if they need one.

`redis_set` and `redis_hset` follow the same rules for keys: a key the action
created is deleted, and an overwritten key is put back as it was (value and
expiry) when an earlier action in the run created it, or with
`managed_teardown = true`. Otherwise an overwritten string is left as written,
and an existing hash loses only the fields the action added. Keys larger than
1 MB aren't restored.

`s3_create_bucket` and `s3_put_object` work the same way: created buckets and
objects are deleted, and before overwriting an object `s3_put_object` copies it to
`.orchard-backup/<run>/<key>` in the same bucket. Teardown copies it back under the
same rules as above, and removes the backup either way.

## `orchard teardown <record>`

Hand the record file to `orchard teardown` and Orchard walks the scenario in
**reverse** topological order, running each action's teardown body:

```sh
orchard teardown orchard_dev_run_20260415T103045Z_a3f29e5b.json
```

For a scenario that inserted a merchant and then a product (FK → merchants),
teardown runs the product `DELETE` first and the merchant `DELETE` second.
Forward order would fail on the FK constraint.

Teardown is **best-effort**: if one action's teardown fails, the walk keeps
going. The joined set of errors is returned at the end. That matches how
cleanup usually works in practice — you want to unwind as much as possible,
not stop at the first stubborn row.

## When teardown isn't the right tool

- **Auto-generated IDs without a record.** Teardown uses the record's captured
  outputs via `self.*`. If you lose the record, the IDs are gone — but the DB
  rows aren't. Pair teardown with a recognisable natural key (a timestamped
  email, a run tag) if you may need to clean up from a lost record.
- **Non-reversible actions.** Sending an email, enqueuing a message, firing a
  webhook — these don't have an inverse. Leave them without a `lifecycle`
  block; teardown skips actions that declare none and have no automatic undo.
- **Bulk isolation-based cleanup.** For heavy scenarios, a cleaner pattern is
  to create resources inside a dedicated isolation boundary (a Postgres
  schema-per-run, a tenant tag) and drop the whole boundary rather than
  deleting rows one by one. Pass the run id through a variable and let each
  action teardown match on it.

## Patterns

- **UAT with scenario-computed identifiers.** Make the identifier a variable
  (or computed via `format()` / `timeadd()` in a default), run the scenario
  with `--record-dir .orchard/runs`, and run teardown later from the recorded
  file. See the [UAT guide](../../guides/uat-testing/).
- **Integration tests.** Run the scenario via a `RecordingReporter` in your
  test harness, then call the engine's `Teardown` between tests. See the
  [integration testing guide](../../guides/integration-testing/).
- **Local dev.** Run `scenarios/dev.hcl` to seed the database, poke around,
  then `orchard teardown orchard_dev_run_*.json` when you're done. Records
  pile up in `.orchard/runs/` if you've configured that directory — clean
  them up with a shell glob.

## What doesn't (yet) exist

- **Re-inserting what a typed delete removed.** Not automatic yet.
- **Scenario-level lifecycle policies** (`on_failure = "rollback"`). Planned;
  not yet implemented.
- **`orchard run --teardown` one-shot.** Also planned.
- **`orchard list runs` / `orchard inspect <id>`**. Also planned. For now,
  records are plain JSON — `jq`, `cat`, and `ls` are your tools.

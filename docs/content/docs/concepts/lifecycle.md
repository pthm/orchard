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
  block; teardown skips actions that don't declare one.
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

- **Managed teardowns.** Built-in actions don't generate their own teardowns
  today. When the typed DB provider lands, `db.insert` will auto-produce its
  `DELETE` without the author writing one.
- **Scenario-level lifecycle policies** (`on_failure = "rollback"`). Planned;
  not yet implemented.
- **`orchard run --teardown` one-shot.** Also planned.
- **`orchard list runs` / `orchard inspect <id>`**. Also planned. For now,
  records are plain JSON — `jq`, `cat`, and `ls` are your tools.

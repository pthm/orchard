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

record: orchard_dev_run_20260415T103045Z_a3f29e5b.json
```

`orchard run` prints the record's path on stderr once it's written.

The record captures:

- The scenario name and the absolute path it ran from.
- The resolved scenario variables.
- Per-node timing and outputs.
- The scenario-level outputs.
- A top-level status: `succeeded` or `failed`.
- Teardown progress, once you've run `orchard teardown` on it.

A run that's interrupted with Ctrl-C (or SIGTERM) still writes its record:
any action in flight is cancelled and recorded as failed, and everything that
finished keeps its outputs and undo. Tear it down like any other run:

```sh
orchard teardown scenarios/dev.hcl --latest
```

A second Ctrl-C exits at once, without writing a record.

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

## Tagging a run's resources

A scenario variable's default is evaluated once, when the scenario loads, and
the run record stores the value. Teardown reloads the scenario with that value rather
than evaluating the default again, so a `uuid()` default gives every run its
own stable tag:

```hcl
variable "run_tag" {
  default = uuid()
}

action "postgres_query" "seed_merchants" {
  query = "INSERT INTO merchants (name, tag) SELECT 'm' || n, '${var.run_tag}' FROM generate_series(1, 50) n"

  lifecycle {
    teardown {
      query = "DELETE FROM merchants WHERE tag = '${var.run_tag}'"
    }
  }
}
```

The same tag works anywhere you name what you create. Prefix Redis keys with
it, or put it in S3 object metadata so the run's objects can be found later:

```hcl
action "redis_set" "session" {
  key   = "${var.run_tag}:session:alice"
  value = "active"
}

action "s3_put_object" "report" {
  bucket   = "fixtures"
  key      = "reports/daily.csv"
  content  = "id,total\n1,10\n"
  metadata = { orchard-run = var.run_tag }
}
```

`orchard inspect <run-id>` shows the run's variables, tag included. To choose
the tag yourself, say to match a CI job, pin it with
`--var run_tag=ci-1234`; the record stores that value instead.

Declare the tag on the scenario and use it in the scenario's own actions. Inside
a component, `var.*` does not resolve in a `lifecycle { teardown { } }` body yet:
the record doesn't store component variables, so teardown fails with an unknown
variable error, even when the scenario passes the tag in through `inputs`.

## `orchard teardown <record>`

Hand the record file, or just its run ID, to `orchard teardown` and Orchard
walks the scenario in **reverse** of the order the run completed its actions
(a reverse topological order), running each action's
teardown body:

```sh
orchard teardown orchard_dev_run_20260415T103045Z_a3f29e5b.json
orchard teardown 20260415T103045Z_a3f29e5b                       # looked up in .
orchard teardown 20260415T103045Z_a3f29e5b --record-dir .orchard/runs
orchard teardown scenarios/dev.hcl --latest                      # newest run of the file
```

With `--latest` the argument is a scenario file, and Orchard picks the newest
record in `--record-dir` for that file that isn't fully torn down. Run it
again to unwind the next one, newest first; failed runs count too. When none
are left it says `no runs left to tear down`. If the file declares several
scenarios and more than one has runs, name one with `--scenario`.

For a scenario that inserted a merchant and then a product (FK → merchants),
teardown runs the product `DELETE` first and the merchant `DELETE` second.
Forward order would fail on the FK constraint.

Teardown reloads the scenario with the variables the recorded run used, not
their current defaults, and `--var` doesn't apply. A scenario run with
`--var rows=3` against an action with `count = var.rows` tears down all three
instances, and a variable with no default needs no override. If the scenario
no longer declares an action the record says created something, teardown
refuses and names it, so put the action back before tearing down. An action
an earlier teardown of the record already tore down can be removed.

Teardown is **best-effort**: if one action's teardown fails, the walk keeps
going. The joined set of errors is returned at the end. That matches how
cleanup usually works in practice — you want to unwind as much as possible,
not stop at the first stubborn row.

Teardown writes its progress back into the record. Each action whose teardown
succeeded is marked `torn_down`, so after fixing whatever failed you run the
same `orchard teardown` again and it picks up where it left off, without
repeating the teardowns that already ran. Once a teardown finishes with no
failures the record gets `torn_down_at`, and a later teardown of it is refused
("already torn down at ...") before any provider is touched.

`orchard list runs` lists records with their status and teardown progress, and
`orchard inspect <run-id>` shows one in full: each node's status, error, the
undo step its provider recorded, and whether it has been torn down.

## When teardown isn't the right tool

- **Auto-generated IDs without a record.** Teardown uses the record's captured
  outputs via `self.*`. If you lose the record, the IDs are gone — but the DB
  rows aren't. Pair teardown with a recognisable natural key (a timestamped
  email, a [run tag](#tagging-a-runs-resources)) if you may need to clean up
  from a lost record.
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
  then `orchard teardown scenarios/dev.hcl --latest` when you're done. Records
  pile up in `.orchard/runs/` if you've configured that directory — clean
  them up with a shell glob.

## What doesn't (yet) exist

- **Re-inserting what a typed delete removed.** Not automatic yet.

There are no scenario-level lifecycle policies (`on_failure = "rollback"`) and
no in-process rollback, by design: a failed or interrupted run still writes its
record, so run `orchard teardown <scenario> --latest` to undo it.

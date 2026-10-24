---
title: CLI reference
linkTitle: CLI
weight: 10
---

```
orchard <command> [arguments] [flags]
```

## Global flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--var key=value` | string map | — | Override a scenario variable. Repeatable: `--var a=1 --var b=2`. |
| `--no-color` | bool | `false` | Disable colored output. |

**Environment variables**

| Name | Effect |
|------|--------|
| `NO_COLOR` | If set to any value, disables colored output. Equivalent to `--no-color`. |

## `orchard init`

```
orchard init [directory]
```

Scaffold a new Orchard project with a starter scenario and component.

| Argument | Description |
|----------|-------------|
| `[directory]` | Where to create the files. Defaults to `.`. Created if it doesn't exist. |

**What it creates**

| File | Purpose |
|------|---------|
| `scenarios/hello.hcl` | A single-component scenario that inserts a merchant row with a lifecycle teardown block. |
| `.orchard/schema/.gitkeep` | Marks the project root. `orchard schema pull` writes database snapshots here. |
| `components/merchant.hcl` | A reusable merchant component with typed inputs, outputs, and teardown. |

**Behavior**

- Existing files are skipped — re-running `init` in a directory that already
  has some files is safe.
- After scaffolding, prints actionable next steps: edit the DSN, plan, run.

**Example**

```sh
orchard init my-project
cd my-project
orchard plan scenarios/hello.hcl --var dsn="postgres://localhost/dev"
orchard run  scenarios/hello.hcl --var dsn="postgres://localhost/dev"
```

## `orchard run`

```
orchard run <scenario>
```

Execute a scenario. Starts providers, runs actions in dependency order, and
prints declared outputs. Exits non-zero on validation or execution failure.

| Argument | Description |
|----------|-------------|
| `<scenario>` | Path to an HCL scenario file. Required. |

**Flags**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--record-dir <path>` | string | `.` (cwd) | Directory to write the run record to. Created if missing. |
| `--no-record` | bool | `false` | Skip writing a run record. |
| `--scenario <name>` | string | — | Pick a scenario by name when the file defines multiple. Optional for single-scenario files. |
| `--seed <n>` | uint | random | Seed for random functions. Overrides the scenario's `seed`. See [random values and seeds](../expressions/#random-values-and-seeds). |
| `--parallelism <n>` | int | `1` | Run up to `n` independent actions at once. Must be at least 1. Actions that touch the same data without referencing each other should use `depends_on`, or the order they change it in is not guaranteed. |

**Behavior**

1. Parse the scenario and any components it references.
2. Register providers: built-ins are instantiated in-process; external providers
   are launched as subprocesses.
3. Send `describe` to every provider and validate schemas.
4. Evaluate provider configuration; send `configure` to every provider.
5. Build the dependency graph and send `execute` for each action once the
   actions it depends on have completed: one at a time in a stable order, or
   up to `--parallelism` independent actions at once. After a failure no new
   action starts; actions already running finish and are recorded.
6. Evaluate scenario outputs and print them.
7. Send `shutdown` to every provider.
8. Write a JSON **run record** to `--record-dir` (unless `--no-record` is set),
   capturing variables, the seed, per-node outputs, and scenario outputs, and
   print `seed: <n>` and `record: <path>` on stderr. See [run records](#run-records) below.

Ctrl-C or SIGTERM cancels the run: actions in flight fail, and the
record is still written with everything that finished, ready for
`orchard teardown <scenario> --latest`. A second Ctrl-C exits immediately.

**Exit codes**

- `0` — scenario ran to completion.
- `1` — any parsing, validation, or execution error, or an interrupted run.

**Output format**

Terminal (TTY) output is styled: checkmarks, timing, color. Piped output is
plain text — one action per line, grep-friendly. Selection is automatic based
on whether stdout is a TTY. `NO_COLOR` or `--no-color` forces plain output.

## `orchard plan`

```
orchard plan <scenario>
```

Validate a scenario and print its execution plan without side effects.

| Argument | Description |
|----------|-------------|
| `<scenario>` | Path to an HCL scenario file. Required. |

**Flags**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--scenario <name>` | string | — | Pick a scenario by name when the file defines multiple. |
| `--show-values` | bool | `false` | List each action's evaluated attributes under it, and print the seed on stderr. |
| `--seed <n>` | uint | random | Seed for random functions. With the same seed, `--show-values` shows the random values `orchard run --seed <n>` will use. |

**Behavior**

1. Parse the scenario and any components it references.
2. Load provider schemas:
   - Built-in providers are instantiated in-process.
   - External providers are probed with `<binary> schema` — a short-lived
     process that prints the schema JSON and exits.
3. Load each provider's schema snapshot from `.orchard/schema/<alias>.json`
   (written by [`orchard schema pull`](#orchard-schema-pull)), if there is one.
4. Validate references, types, and required attributes across the scenario,
   inside components as well as at scenario level. Typed database actions
   (`*_insert`, `*_update`, `*_delete`) are checked against their snapshot:
   the table and columns exist, values fit their columns, required columns are
   set, generated ones aren't. References to an insert's outputs are checked
   against the table's columns, so `action.postgres_insert.merchant.idd` fails.
5. Build the dependency graph and print it.

Plan never calls `configure` or `execute` on any provider, and never connects
to a database; snapshots are read from disk. It is safe to run against a
scenario that points at a production system.

Plan also prints hints, which never fail it: for instance when a typed action
sets a foreign key to a fixed value although the scenario inserts into the
referenced table earlier.

If a provider has typed actions but no snapshot, plan skips those checks and
prints a note saying so. `orchard run` always checks typed actions against the
live database before executing anything.

**Showing values**

With `--show-values`, plan lists each action's attributes as they evaluate,
schema defaults included:

```
action "sqlite_insert" "merchant"  via db
  on_conflict = "error"
  table       = "merchants"
  values      = { name = "Acme" }
  action "sqlite_insert" "product"  via db
    on_conflict = "error"
    table       = "products"
    values      = { merchant_id = (known after run), name = "Widget" }
```

A value that depends on another action's outputs shows as `(known after run)`,
as do a component's variables inside the component. Functions such as `uuid()`,
`now()` and `env()` are evaluated at plan time, so the values `run` uses can
differ from the ones shown.

**Exit codes**

- `0` — scenario is valid.
- `1` — any validation error.

See [plan vs run](/docs/concepts/plan-vs-run/) for the rationale behind the split.

## `orchard teardown`

```
orchard teardown <record-file | run-id> [--record-dir <path>]
orchard teardown <scenario-file> --latest [--record-dir <path>] [--scenario <name>]
```

Tear down the state created by a recorded scenario run. Reads the JSON record
produced by a prior `orchard run`, reloads the scenario at the path stored in
the record, and evaluates each action's `lifecycle { teardown { } }` body in
**reverse** of the order the run completed its actions (a reverse topological
order).

| Argument | Description |
|----------|-------------|
| `<record-file \| run-id>` | Path to a record written by `orchard run`, or its run ID (`20260415T103045Z_a3f29e5b`) looked up in `--record-dir`. Required. |
| `<scenario-file>` | With `--latest`, the scenario file whose runs to tear down. |

**Flags**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--record-dir <path>` | string | `.` (cwd) | Directory to look a run ID or `--latest` run up in. Ignored when the argument is an existing record file. |
| `--latest` | bool | `false` | Treat the argument as a scenario file and tear down the newest record in `--record-dir` for it that isn't fully torn down (failed runs included). Prints `tearing down run <id> (<record path>)` first. Repeat to unwind runs newest first; errors with `no runs left to tear down for <path> in <dir>` when none remain. |
| `--scenario <name>` | string | — | With `--latest`, which scenario to pick when runs of several scenarios in the file match. Required in that case. |
| `--ignore-config-drift` | bool | `false` | Tear down even when a provider config differs from the one the run used, e.g. after a password rotation. |

**Behavior**

1. Load and decode the record. Refuse a record whose format isn't `1`, and one
   already fully torn down (`already torn down at <time>`).
2. Load the scenario at the absolute path stored in the record, with the
   variables the recorded run used (`--var` does not apply), so `count` /
   `for_each` expand to the same instances, variables without a default
   resolve, and a default such as `uuid()` keeps the value the run recorded.
3. Verify the scenario name matches the one in the record (reject if the file
   has been rewritten to declare a different scenario), and that every
   recorded node with outputs or an undo step, and not yet torn down, is still
   in the scenario (reject and name the missing nodes otherwise).
4. Evaluate the scenario's current provider config blocks against the
   recorded variables, and compare them with the hashes the run recorded
   (`config_hashes`). Refuse and name each changed attribute
   (`provider config changed since the run: db.dsn`) unless
   `--ignore-config-drift` is set. Records without hashes skip the check.
   Then register and configure the providers.
5. Walk the recorded actions in reverse of the order they completed (so after
   a `--parallelism` run, independent actions are undone in the reverse of the
   order they really ran); for each action that succeeded in the
   recorded run and still routes to a registered provider, run its
   `lifecycle { teardown { } }` body if it has one, or otherwise the undo step
   its provider recorded (a typed insert's delete by primary key, or a typed
   update's previous values), unless the action sets `managed_teardown = false`.
6. Skip actions with neither, actions whose original run failed, actions an
   earlier teardown of this record already tore down, and component nodes.
7. Save progress back to the record: mark each action whose teardown
   succeeded `torn_down`, and set `torn_down_at` if nothing failed. A failed
   save is a warning, not an error.

Teardown is **best-effort**: a failure on one action doesn't abort the walk.
The joined set of errors is returned at the end.

**Exit codes**

- `0` — every attempted teardown succeeded.
- `1` — one or more teardowns failed, or the record / scenario could not be
  loaded.

See [lifecycle](/docs/concepts/lifecycle/) for authoring guidance.

## `orchard schema pull`

```
orchard schema pull <scenario> [--schemas a,b] [--force] [--check]
```

Snapshot the schema of each database the scenario uses. Orchard connects to
each provider the same way `orchard run` does, from the scenario's `provider`
blocks and `--var` values, and reads the database's catalog: tables (not views),
columns with their types, nullability, defaults and generated status, primary
and foreign keys, enum values, and comments. No data is read, and no connection
details are stored.

| Argument / flag | Description |
|-----------------|-------------|
| `<scenario>` | Scenario whose providers to snapshot. Required. |
| `--scenario <name>` | Pick a scenario by name when the file defines multiple. |
| `--schemas a,b` | Postgres only: capture just these schemas. Useful when extensions add schemas full of internal tables. |
| `--force` | Replace a snapshot even if it's of a different database. |
| `--check` | Write nothing: compare each snapshot with the database, list the differences, and fail if any snapshot is missing or out of date. For CI. |

**Behavior**

1. Load the scenario and configure its providers, as `orchard run` does.
2. For each database provider (`postgres`, `mysql`, `sqlite`), read its catalog.
3. Write the snapshot to `.orchard/schema/<alias>.json`, where `<alias>` is the
   provider's name in `required_providers`. `.orchard/` is the nearest one
   above the scenario file; if there isn't one, it's created in the current
   directory.
4. Print each snapshot's path and what it captured (`postgres/app_dev`).
   `unchanged` means the schema matched the existing snapshot, which is left
   untouched so re-pulling doesn't create a diff.

If a snapshot already exists for the alias but was taken from a different
database (two scenarios using one alias for different databases), `pull`
refuses to replace it. Rename the alias in one scenario, or pass `--force`.

Commit snapshots so `orchard plan` can check scenarios against them without a
database, and re-run `pull` after migrations. If you forget, `orchard run` warns
when a provider's snapshot no longer matches its database, and
`orchard schema pull --check` fails, listing what changed:

```
  out of date .orchard/schema/db.json (postgres/app_dev):
    - column "public.products.price": type numeric(10,2) → numeric(12,2)
    - table "public.invoices" added
```

**Exit codes**

- `0` — every snapshot was written or unchanged.
- `1` — a provider failed to connect or read its catalog, the scenario has no
  database provider, or the overwrite check failed.

## `orchard list`

```
orchard list [directory]
```

Walk a directory tree and print every `scenario` block found in `.hcl` files.
Files with multiple scenarios list each one as a separate entry.

| Argument | Description |
|----------|-------------|
| `[directory]` | Directory to search. Defaults to `.`. |

**Behavior**

- Walks recursively from `[directory]`.
- Reads every `.hcl` file and extracts `scenario "..." { }` blocks.
- For each scenario, prints the name (bold), tags in violet brackets, file
  path in gray, and description (if present) on the next line.
- Skips the `.seed` directory if present.
- Unparseable files are skipped silently (warnings go to stderr).

**Example output**

```
  hello  [starter]  (scenarios/hello.hcl)
  Insert a merchant and verify the pipeline works end-to-end
```

**Exit codes**

- `0` — walk succeeded. Output may be empty.
- `1` — filesystem error.

## `orchard list runs`

```
orchard list runs [--record-dir .]
```

List the run records `orchard run` wrote, newest first, one line per run.

| Flag | Description |
|------|-------------|
| `--record-dir` | Directory to read run records from. Defaults to `.`. |

**Behavior**

- Each line shows the run ID, scenario name (bold), status (green
  `succeeded`, red `failed`), a `torn down` or `partly torn down` marker
  once `orchard teardown` has run, the start time (UTC) and node counts
  by status.
- Records that can't be loaded are skipped with a warning on stderr.
- An empty directory prints `No runs found in <dir> (see --record-dir)`.
- `--no-color` (or `NO_COLOR`) prints plain text.
- A directory named `runs` can still be listed for scenarios as
  `orchard list ./runs`.

**Example output**

```
  20260415T100512Z_9f2c1a7b  dev  failed  2026-04-15T10:05:12Z  1 succeeded, 1 failed
  20260415T093001Z_4be03d11  dev  succeeded  torn down  2026-04-15T09:30:01Z  2 succeeded
```

**Exit codes**

- `0` — listing succeeded. Output may say no runs were found.
- `1` — filesystem error.

## `orchard inspect`

```
orchard inspect <run-id | record-file> [--record-dir .]
```

Show everything one run record says.

| Argument / flag | Description |
|-----------------|-------------|
| `<run-id \| record-file>` | A run ID looked up in `--record-dir`, or a path to a record file. Required. |
| `--record-dir` | Directory to look a run ID up in. Defaults to `.`. Ignored when the argument is an existing file. |

**Behavior**

- The header shows the run ID, scenario name and path, status, start time
  (UTC), duration, seed, `Torn down` time once `orchard teardown` has finished,
  and the run's error if it failed.
- Variables are listed sorted by name, then nodes in record order with
  their status, ID and duration, plus their error, the undo step their
  provider recorded, and a `torn down` marker where they apply.
- Scenario outputs come last.
- Values print as their raw record JSON, one line each.
- A failed run ends its node list with a note that nodes not listed did
  not run.

**Example output**

```
  ID          20260415T093001Z_4be03d11
  Scenario    dev  (/home/me/project/scenarios/dev.hcl)
  Status      succeeded
  Started     2026-04-15T09:30:01Z
  Duration    12ms

  Variables
    who = "Acme"

  Nodes
    succeeded  action.sqlite_insert.acme  3ms
      undo: sqlite_delete {"table":"merchants","where":{"id":1}}

  Outputs
    id = 1
```

**Exit codes**

- `0` — the record was found and printed.
- `1` — no such record file or run ID, or the record could not be loaded.

## Examples

Scaffold a new project and run it:

```sh
orchard init my-project
cd my-project
orchard run scenarios/hello.hcl --var dsn="postgres://localhost/dev"
```

Run with variable overrides:

```sh
orchard run scenarios/dev.hcl \
  --var dsn="postgres://localhost/dev" \
  --var merchant_name="Acme"
```

Run a specific scenario from a multi-scenario file:

```sh
orchard run scenarios/uat.hcl --scenario subscription_three_failures
```

Plan in CI, fail the build on invalid scenarios:

```sh
for f in scenarios/*.hcl; do
  orchard plan "$f" || exit 1
done
```

List every scenario in a repo:

```sh
orchard list .
```

List past runs, inspect one and tear it down by its ID:

```sh
orchard list runs --record-dir .orchard/runs
orchard inspect 20260415T093001Z_4be03d11 --record-dir .orchard/runs
orchard teardown 20260415T093001Z_4be03d11 --record-dir .orchard/runs
```

Run and tear down in one session:

```sh
orchard run scenarios/uat.hcl --var customer=uat+$(date +%s)@example.com
# use the created state...
orchard teardown scenarios/uat.hcl --latest
```

## Run records

Every successful, failed or interrupted `orchard run` writes a JSON record to `--record-dir`
(default `.`). The filename is `orchard_<scenario>_run_<id>.json`, where `<id>`
is a sortable `YYYYMMDDTHHMMSSZ_<8hex>` string, so lexicographic sort matches
chronological order.

Records are written atomically (temp + rename) and contain:

- `format` — record format version (`1`). Readers refuse any other format.
- `id`, `scenario.name`, `scenario.path` — what ran, where from.
- `started_at`, `finished_at`, `status` (`succeeded` / `failed`) — when and how
  it went.
- `variables` — the resolved variables the scenario ran with.
- `seed` — the seed random functions drew from. `orchard run --seed <seed>`
  draws the same values again; `orchard teardown` reuses it for teardown
  bodies. Absent in records written before seeds existed.
- `nodes` — per-node entries: id, kind, status, timing, and outputs (or error).
- `outputs` — scenario-level outputs.
- `error` — top-level error if the run aborted.
- `torn_down` (per node) and `torn_down_at` — teardown progress, written back
  by `orchard teardown`. Absent until a teardown runs.

Records power `orchard teardown`, which replays the scenario in reverse using
each action's `lifecycle { teardown { } }` block. Records are not required for
`run` to succeed — pass `--no-record` to skip writing them, e.g. in CI paths
where you clean up through a different mechanism.

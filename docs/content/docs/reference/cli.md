---
title: CLI
weight: 10
---

# CLI reference

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

**Behavior**

1. Parse the scenario and any components it references.
2. Register providers: built-ins are instantiated in-process; external providers
   are launched as subprocesses.
3. Send `describe` to every provider and validate schemas.
4. Evaluate provider configuration; send `configure` to every provider.
5. Build the dependency graph, topologically sort, and send `execute` for each
   action in order.
6. Evaluate scenario outputs and print them.
7. Send `shutdown` to every provider.
8. Write a JSON **run record** to `--record-dir` (unless `--no-record` is set),
   capturing variables, per-node outputs, and scenario outputs. See
   [run records](#run-records) below.

**Exit codes**

- `0` — scenario ran to completion.
- `1` — any parsing, validation, or execution error.

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

**Behavior**

1. Parse the scenario and any components it references.
2. Load provider schemas:
   - Built-in providers are instantiated in-process.
   - External providers are probed with `<binary> schema` — a short-lived
     process that prints the schema JSON and exits.
3. Validate references, types, and required attributes across the scenario.
4. Build the dependency graph and print it.

Plan never calls `configure` or `execute` on any provider. It is safe to run
against a scenario that points at a production system.

**Exit codes**

- `0` — scenario is valid.
- `1` — any validation error.

See [plan vs run](../../concepts/plan-vs-run/) for the rationale behind the split.

## `orchard teardown`

```
orchard teardown <record-file>
```

Tear down the state created by a recorded scenario run. Reads the JSON record
produced by a prior `orchard run`, reloads the scenario at the path stored in
the record, and evaluates each action's `lifecycle { teardown { } }` body in
**reverse** topological order.

| Argument | Description |
|----------|-------------|
| `<record-file>` | Path to a record written by `orchard run`. Required. |

**Behavior**

1. Load and decode the record file.
2. Load the scenario at the absolute path stored in the record.
3. Verify the scenario name matches the one in the record (reject if the file
   has been rewritten to declare a different scenario).
4. Register and configure providers using the scenario's current config blocks.
5. Walk the dependency graph in reverse; for each action that (a) has a
   `lifecycle { teardown { } }` block, (b) succeeded in the recorded run, and
   (c) still routes to a registered provider, evaluate the teardown body and
   dispatch it through the provider.
6. Skip actions without teardown blocks, actions whose original run failed, and
   component nodes.

Teardown is **best-effort**: a failure on one action doesn't abort the walk.
The joined set of errors is returned at the end.

**Exit codes**

- `0` — every attempted teardown succeeded.
- `1` — one or more teardowns failed, or the record / scenario could not be
  loaded.

See [lifecycle](../../concepts/lifecycle/) for authoring guidance.

## `orchard list`

```
orchard list [directory]
```

Walk a directory tree and print every `scenario` block found in `.hcl` files.

| Argument | Description |
|----------|-------------|
| `[directory]` | Directory to search. Defaults to `.`. |

**Behavior**

- Walks recursively from `[directory]`.
- Reads every `.hcl` file and extracts `scenario "..." { }` blocks.
- Prints `scenario_name  path/to/file.hcl`, one per line.
- Skips the `.seed` directory if present.
- Unparseable files are skipped silently (warnings go to stderr).

**Exit codes**

- `0` — walk succeeded. Output may be empty.
- `1` — filesystem error.

## Examples

Run with variable overrides:

```sh
orchard run scenarios/dev.hcl \
  --var dsn="postgres://localhost/dev" \
  --var merchant_name="Acme"
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

Run and tear down in one session:

```sh
orchard run scenarios/uat.hcl --var customer=uat+$(date +%s)@example.com
# use the created state...
orchard teardown orchard_uat_run_*.json
```

## Run records

Every successful or failed `orchard run` writes a JSON record to `--record-dir`
(default `.`). The filename is `orchard_<scenario>_run_<id>.json`, where `<id>`
is a sortable `YYYYMMDDTHHMMSSZ_<8hex>` string, so lexicographic sort matches
chronological order.

Records are written atomically (temp + rename) and contain:

- `format` — record format version, for future readers.
- `id`, `scenario.name`, `scenario.path` — what ran, where from.
- `started_at`, `finished_at`, `status` (`succeeded` / `failed`) — when and how
  it went.
- `variables` — the resolved variables the scenario ran with.
- `nodes` — per-node entries: id, kind, status, timing, and outputs (or error).
- `outputs` — scenario-level outputs.
- `error` — top-level error if the run aborted.

Records power `orchard teardown`, which replays the scenario in reverse using
each action's `lifecycle { teardown { } }` block. Records are not required for
`run` to succeed — pass `--no-record` to skip writing them, e.g. in CI paths
where you clean up through a different mechanism.

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

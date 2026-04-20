<p align="center">
  <img src="docs/static/orchard_logo.png" alt="Orchard" width="420">
</p>

<p align="center">
  <strong>Define reproducible application state as code.</strong><br>
  Scenario-based data orchestration driven by HCL.
</p>

<p align="center">
  <a href="https://orchard.pthm.dev/">Documentation</a>
  &nbsp;·&nbsp;
  <a href="https://orchard.pthm.dev/docs/getting-started/">Getting started</a>
  &nbsp;·&nbsp;
  <a href="https://orchard.pthm.dev/docs/providers/">Providers</a>
  &nbsp;·&nbsp;
  <a href="https://orchard.pthm.dev/docs/extending/">Extending</a>
</p>

---

## What is Orchard?

Orchard is a CLI that lets teams describe complex application state — a merchant
with three failed payments, an org with SSO enabled and a suspended user, a
fully-seeded local dev database — and builds it by inserting rows, calling APIs,
and wiring outputs between steps automatically.

Instead of writing ad-hoc seed scripts, you write **scenarios** in HCL. Orchard
plans them, shows the execution graph, and (when you're ready) runs them through
a pluggable provider system. Built-in providers cover PostgreSQL, HTTP, and
shell execution; custom providers are any binary that speaks Orchard's
JSON-over-stdio protocol.

### Why

- **Reproducibility.** Same inputs, same state. Scenarios are version-controlled
  and reviewable; seed scripts usually aren't.
- **Composition.** Scenarios compose *components* — reusable units of state with
  typed inputs and outputs.
- **Plan before run.** `orchard plan` type-checks every reference, resolves the
  dependency graph, and prints what would execute without touching external
  systems.

## A quick look

```hcl
scenario "hello" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {
    default = "postgres://localhost:5432/orchard_dev?sslmode=disable"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  action "postgres_query" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us') RETURNING id, name"
  }

  action "postgres_query" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.postgres_query.create_merchant.id}, 'Widget', 9.99) RETURNING id"
  }

  output "merchant_id" { value = action.postgres_query.create_merchant.id }
  output "product_id"  { value = action.postgres_query.create_product.id }
}
```

```sh
orchard plan scenarios/hello.hcl    # dry-run; no side effects
orchard run  scenarios/hello.hcl    # executes and prints outputs
```

## Install

From source (requires Go 1.22+):

```sh
go install github.com/pthm/orchard@latest
```

Verify:

```sh
orchard --help
```

Prebuilt binaries are not yet published.

## CLI at a glance

| Command | Purpose |
|---------|---------|
| `orchard run <scenario>` | Execute a scenario end-to-end. |
| `orchard plan <scenario>` | Validate and print the execution plan. Read-only; safe in CI. |
| `orchard list [dir]` | List every `scenario` block found under a directory. |
| `orchard docs <source>` | Render provider reference docs from a provider's schema. Works on `builtin/*` and `exec:<path>`. |

Global flags:

- `--var key=value` — override a scenario variable (repeatable).
- `--no-color` — disable coloured output (honours `NO_COLOR`).

Full reference at [orchard.pthm.dev/docs/reference/cli/](https://orchard.pthm.dev/docs/reference/cli/).

## Built-in providers

| Source | Actions | What it does |
|--------|---------|--------------|
| `builtin/postgres` | `postgres_query`, `postgres_copy` | Execute SQL or bulk-load CSV via COPY; capture outputs. |
| `builtin/http` | `http_request` | Issue HTTP requests; capture status, body, and headers. |
| `builtin/exec` | `exec_run` | Run a shell command; capture stdout, stderr, and exit code. |

Custom providers are any binary that implements the provider protocol. See
[Extending Orchard](https://orchard.pthm.dev/docs/extending/) for the wire
format, lifecycle, and a worked tutorial.

## Repo layout

```
cmd/               Cobra subcommands (run, plan, list, docs)
internal/
  parser/          HCL decode, variable resolution, provider/component bindings
  graph/           Dependency graph construction and topological sort
  engine/          Runtime: evaluation, provider dispatch, outputs
  docs/            Markdown renderer for the `orchard docs` subcommand
pkg/
  protocol/        Wire protocol types (ProviderSchema, envelopes, cty helpers)
  provider/        Provider interface, builtin registry, external (stdio) client
    postgres/      Built-in PostgreSQL provider
    http/          Built-in HTTP provider
    exec/          Built-in shell provider
docs/              Hugo source for https://orchard.pthm.dev
test/integration/  Full-stack scenarios against a real postgres + echo provider
```

## Development

This project uses [`just`](https://github.com/casey/just) as its task runner.

```sh
just                   # list recipes
just test              # run the Go tests
just test-all          # Go tests + integration suite
just docs              # regenerate provider docs + build the Hugo site
just docs-watch        # live-reload docs site
just docs-provider p   # regenerate one provider's reference page
```

The Hugo site pulls generated provider reference pages from
`docs/content-generated/`, which is gitignored — run `just docs-generate` after
editing any `pkg/provider/*/schema.hcl`.

## Contributing

Issues and pull requests welcome. Before proposing a change:

1. `just test` passes.
2. `go vet ./...` is clean.
3. New behaviour has a test. User-visible changes update the docs under
   `docs/content/docs/`.

Provider authors — both built-in and external — should read
[Extending Orchard](https://orchard.pthm.dev/docs/extending/) first: it covers
the wire protocol, lifecycle, schema model, and publishing conventions that
downstream tooling (plan, docs generator, external clients) assumes.

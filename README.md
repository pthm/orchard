<p align="center">
  <img src="docs/static/images/orchard-dark.png" alt="Orchard" width="420">
</p>

<p align="center">
  <strong>Seed more than your database.</strong><br>
  Describe your application state once, in reusable HCL components. Orchard builds it
  with the tools you already have, and tears it down when you're done.
</p>

<p align="center">
  <a href="https://orchard.pthm.dev/">Documentation</a>
  &nbsp;·&nbsp;
  <a href="https://orchard.pthm.dev/docs/getting-started/">Getting started</a>
  &nbsp;·&nbsp;
  <a href="https://orchard.pthm.dev/providers/">Providers</a>
  &nbsp;·&nbsp;
  <a href="https://orchard.pthm.dev/docs/extending/">Extending</a>
</p>

---

Orchard builds application state from code: a merchant with three failed payments,
an org with SSO on and a suspended user, a fully seeded local environment. You
describe it in HCL **scenarios**; Orchard works out the order, runs each step with
the right tool (SQL, Redis, S3, HTTP and gRPC calls, shell commands), passes outputs
from one step to the next, and records what it did so it can undo it.

Write it once and use it for demos, integration tests and local development.

- **Plan before you run.** `orchard plan` type-checks every reference and, from a
  committed schema snapshot, every typed insert, update and gRPC call, without
  touching anything.
- **Compose.** Components are reusable units of state with typed inputs and
  outputs. Scenarios assemble them.
- **Clean up.** `orchard teardown` undoes a run. Rows, keys and objects Orchard
  created are deleted and values it overwrote are put back automatically; any
  other step can declare its own teardown.
- **Bring your own tools.** Any binary that speaks Orchard's JSON-over-stdio
  protocol can be a provider.

## A quick look

```hcl
scenario "demo_merchant" {
  required_providers {
    postgres = { source = "builtin/postgres" }
    redis    = { source = "builtin/redis" }
    api      = { source = "builtin/http" }
  }

  variable "name" { default = "Acme" }

  provider "postgres" { dsn = "postgres://localhost:5432/shop?sslmode=disable" }
  provider "redis"    { url = "redis://localhost:6379/0" }
  provider "api"      { base_url = "http://localhost:3000" }

  # Schema-checked inserts: a typo'd column fails before anything runs.
  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = var.name, region = "eu" }
  }

  action "postgres_insert" "product" {
    table  = "products"
    values = { merchant_id = action.postgres_insert.merchant.id, name = "Widget", price = 9.99 }
  }

  # Outputs flow between steps, across tools.
  action "redis_hset" "flags" {
    key    = "flags:${action.postgres_insert.merchant.id}"
    fields = { new_checkout = true }
  }

  action "http_request" "reindex" {
    method = "POST"
    url    = "/admin/merchants/${action.postgres_insert.merchant.id}/reindex"
  }

  output "merchant_id" { value = action.postgres_insert.merchant.id }
}
```

```sh
orchard plan scenarios/demo.hcl                    # check everything; no side effects
orchard run scenarios/demo.hcl                     # build it, and write a run record
orchard teardown orchard_demo_merchant_run_*.json  # undo it
```

## Install

Homebrew (macOS and Linux):

```sh
brew install pthm/tap/orchard
```

Scoop (Windows):

```sh
scoop bucket add pthm https://github.com/pthm/scoop-bucket
scoop install pthm/orchard
```

With Go (requires Go 1.26+):

```sh
go install github.com/pthm/orchard@latest
```

Prebuilt binaries for Linux, macOS, and Windows (`.tar.gz`/`.zip`), plus
`.deb` and `.rpm` packages for Linux, are attached to each
[GitHub release](https://github.com/pthm/orchard/releases) alongside
`checksums.txt`.

Verify:

```sh
orchard version
```

## CLI at a glance

| Command | Purpose |
|---------|---------|
| `orchard init` | Scaffold a new project. |
| `orchard plan <scenario>` | Validate a scenario and print its execution plan. Read-only; safe in CI. |
| `orchard run <scenario>` | Execute a scenario and write a run record. |
| `orchard teardown <record>` | Undo a recorded run, in reverse order. |
| `orchard schema pull <scenario>` | Snapshot the schemas of the databases and gRPC services a scenario uses, for plan-time checks. |
| `orchard list [dir]` | List the scenarios under a directory. |
| `orchard functions` | List the HCL functions available in scenarios. |
| `orchard docs <source>` | Render a provider's reference docs. |

Pass `--var key=value` to set scenario variables, and `--no-color` (or `NO_COLOR`)
for plain output. Full reference at [orchard.pthm.dev/reference/cli/](https://orchard.pthm.dev/reference/cli/).

## Built-in providers

| Source | Actions | What it does |
|--------|---------|--------------|
| `builtin/postgres` | `postgres_query`, `postgres_copy`, `postgres_insert`, `postgres_update`, `postgres_delete` | Execute SQL, bulk-load CSV via COPY, or run schema-checked inserts, updates and deletes. |
| `builtin/mysql` | `mysql_query`, `mysql_insert`, `mysql_update`, `mysql_delete` | Execute SQL against MySQL or MariaDB, or run schema-checked inserts, updates and deletes. |
| `builtin/sqlite` | `sqlite_query`, `sqlite_insert`, `sqlite_update`, `sqlite_delete` | Execute SQL against a database file, or run schema-checked inserts, updates and deletes. |
| `builtin/redis` | `redis_command`, `redis_set`, `redis_hset` | Set keys and hashes, or run any command, on Redis, Valkey and compatible stores. |
| `builtin/s3` | `s3_create_bucket`, `s3_put_object`, `s3_delete_object`, `s3_delete_bucket` | Create buckets and upload objects on S3, S3-compatible stores and Google Cloud Storage. |
| `builtin/grpc` | `grpc_call` | Call gRPC methods with no generated code, using server reflection or a descriptor set. |
| `builtin/http` | `http_request` | Issue HTTP requests; capture status, body, and headers. |
| `builtin/exec` | `exec_run` | Run a shell command; capture stdout, stderr, and exit code. |

Custom providers are any binary that implements the provider protocol. See
[Extending Orchard](https://orchard.pthm.dev/docs/extending/) for the wire
format, lifecycle, and a worked tutorial.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for
the development setup.

## License

[MIT](LICENSE)

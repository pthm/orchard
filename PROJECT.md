# Orchard

**Define reproducible application state as code.**

Orchard is a CLI tool that lets teams define, compose, and execute *scenarios* — declarative descriptions of complex application state — using HCL. Instead of writing ad-hoc seeding scripts, teams describe what state they want and Orchard builds it: inserting database rows, calling APIs, running scripts, and wiring outputs between steps automatically.

The tool sits at the intersection of fixture libraries, test scenario runners, lightweight workflow engines, and environment bootstrap tools. It is not just a CSV importer or a SQL loader. It is a **state orchestration tool** that models the reality that meaningful application state is not just rows in tables — it is workflows, side effects, histories, and relationships across services.

---

## Goals

Orchard exists because real dev/test/demo environments need more than "insert some rows." They need:

- **Relational consistency** — a merchant needs accounts, accounts need transactions, transactions need settlements
- **Service-to-service effects** — syncing external systems, triggering jobs, calling APIs
- **Realistic histories** — not just current state, but the sequence of events that produced it
- **Reusability** — the same "create a merchant" logic used across dozens of scenarios
- **Composability** — build complex states from simple, tested building blocks
- **Reproducibility** — run the same scenario twice, get the same result
- **Easy cleanup** — tear down what was created without hunting through tables manually

The tool should make it trivial to say things like:

- "Give me a merchant with 3 failed payments and a pending review"
- "Give me an org with SSO enabled and one suspended user"
- "Give me a customer who imported transactions yesterday and triggered alerts today"

That is **scenario language**, not fixture language.

---

## Target Users

Orchard is strongest for teams with:

- Multiple services with complex data relationships
- Event-driven flows where state is the result of a sequence of operations
- Hard-to-reproduce states that require specific data across multiple tables and systems
- Heavy local dev, QA, or demo setup pain
- Fintech, SaaS, marketplaces, operations platforms, and data-heavy applications

---

## Core Concepts

### Scenario (Top-level entrypoint)

A **Scenario** defines a complete, reproducible system state. It is the thing a user runs. It composes components, accepts parameters via variables, and produces outputs. A scenario is the root module — the named entrypoint that describes "the state I want."

```hcl
scenario "merchant_with_chargeback" {
  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "dsn" {
    default = "postgres://localhost:5432/orchard?sslmode=disable"
  }

  variable "merchant_name" {
    default = "Acme Corp"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "base" {
    source = "../components/merchant.hcl"
    inputs = {
      name = var.merchant_name
    }
  }

  component "transactions" "settled" {
    source = "../components/transactions.hcl"
    inputs = {
      merchant_id = component.merchant.base.id
      count       = 25
    }
  }

  output "merchant_id" {
    value = component.merchant.base.id
  }
}
```

Scenarios declare which providers they need via `required_providers` and configure each one with a `provider "<name>" {}` block. The `source` string resolves through the provider registry: `builtin/<name>` picks a compiled-in provider, `exec:<path>` spawns an external subprocess (see [Provider Plugin System](#provider-plugin-system)).

### Component (Reusable module)

A **Component** is a reusable, parameterized building block — like a Terraform module. It encapsulates the logic for creating a piece of state: declaring inputs (variables), executing actions, and exposing outputs. Components have a clear interface contract: callers pass inputs, the component does its work internally, and only declared outputs are visible to the outside.

This encapsulation is critical. A component like `merchant` might internally insert rows, call an API, and trigger a sync job. The caller does not care how — they just get back `merchant.id` and `merchant.name`.

```hcl
component "merchant" {
  variable "name" {
    type = string
  }

  variable "region" {
    type    = string
    default = "us"
  }

  action "sql" "create" {
    query = <<-SQL
      INSERT INTO merchants (name, region)
      VALUES ('${var.name}', '${var.region}')
      RETURNING id, name, region
    SQL
  }

  output "id" {
    value = action.sql.create.id
  }

  output "name" {
    value = action.sql.create.name
  }
}
```

### Action (Executable unit)

An **Action** is the smallest executable unit. It performs a side effect — running SQL, calling an HTTP endpoint, executing a command — and produces outputs that downstream actions and components can reference. Actions use a two-label syntax where the first label is the action type (which selects the provider) and the second is the instance name:

```hcl
action "sql" "create_merchant" { ... }
action "cmd" "generate_data" { ... }
action "http" "trigger_sync" { ... }
```

This Terraform-style syntax means different action types can have different schemas (a `sql` action has a `query` field; an `http` action has `method`, `url`, `body`), and the type is structural rather than a stringly-typed `action = "sql.exec"` attribute.

### Hierarchy

```
Scenario
  ├── Component (instance)
  │     ├── Action
  │     ├── Action
  │     └── Outputs
  ├── Component (instance)
  ├── Action (scenario-level)
  └── Outputs
```

A scenario describes **what state you want**. Components describe **how parts of that state are built**. Actions perform **the actual work**.

---

## Language Design

Orchard uses [HCL](https://github.com/hashicorp/hcl) (HashiCorp Configuration Language) because the domain maps naturally to it:

- Scenarios are root modules
- Components are child modules with inputs/outputs
- The reference system (`component.merchant.base.id`) maps directly to HCL's traversal expressions
- HCL's heredoc strings make inline SQL ergonomic
- String interpolation (`${var.name}`) provides expression embedding within strings
- The block structure makes scenario definitions readable and inspectable

### Referencing Model

References follow structured, typed paths:

```
component.<type>.<name>.<output>    — component.merchant.base.id
action.<type>.<name>.<output>       — action.sql.create.id
var.<name>                          — var.merchant_name
```

These references serve double duty:

1. **Dependency graph construction** — at parse time, Orchard inspects all expressions for variable references (using HCL's `Expression.Variables()`) to build an implicit execution graph. If action B references `action.sql.A.id`, then A must execute before B.
2. **Value resolution** — at execution time, after upstream nodes have executed, their outputs are placed into an `EvalContext` and downstream expressions are evaluated against it.

### Variables and Types

Variable blocks accept a `type` constraint using HCL's native type-expression syntax:

```hcl
variable "merchant_name" {
  type    = string
  default = "Acme Corp"
}

variable "transaction_count" {
  type    = number
  default = 10
}

variable "tags" {
  type    = list(string)
  default = []
}
```

Types are optional. A variable without a `type` accepts any value the default or override produces (`cty.DynamicPseudoType`). When a type is declared:

- **Defaults** are coerced to the declared type at load time; a mismatch is a parse-time error, not a runtime surprise.
- **CLI overrides** are HCL-parsed against the declared type. `--var count=42` produces a number, `--var flag=true` produces a bool, `--var tags=["a","b"]` produces a list. String-typed (or untyped) variables take the raw override verbatim, so `--var name=Acme Corp` still works without quoting.
- **Component inputs** are coerced to the declared type of the matching component variable. Stray inputs with no matching variable are rejected; required variables with no default that receive no input are rejected.

Error messages identify the offending variable, the declared type, and the value that failed to coerce.

### Two-Pass HCL Evaluation

This is the core architectural decision. HCL expressions cannot be fully evaluated at parse time because their values depend on execution results (e.g., a database-generated ID). Orchard uses a two-pass approach:

**Pass 1 — Structural decode (parse time):** Decode the HCL block structure (scenario names, component labels, action types) but capture expression-valued fields as unevaluated `hcl.Expression` handles. Use `Expression.Variables()` to extract dependency references for the graph — this works without any values being resolved.

**Pass 2 — Expression evaluation (execution time):** For each node in topological order, build an `hcl.EvalContext` populated with all resolved upstream outputs and variables, then call `Expression.Value(ctx)` to evaluate deferred expressions. This preserves HCL's full expression language — string interpolation, functions, type coercion — while enabling dependency-driven execution.

---

## Provider Plugin System

Providers extend Orchard's action vocabulary. A provider declares a schema of the actions it offers, configures itself from the scenario, and executes actions against its target system (a database, an HTTP API, a command shell, a custom service). Orchard's core stays focused on the orchestration problem — parsing, dependency resolution, expression evaluation — and providers handle the "do the thing" side.

Two kinds of providers share one interface:

- **Builtin providers** are compiled into the `orchard` binary and registered at startup. Today's builtins are `postgres` (raw SQL) and `http` (HTTP requests).
- **External providers** are separate subprocesses that Orchard spawns and talks to over JSON Lines on stdio. Any language that can read and write JSON is a valid host — a Python, Rust, or Node provider is roughly the same shape as the example Go one.

Both kinds implement the same Go interface:

```go
type Provider interface {
    Schema() ProviderSchema
    Configure(ctx, config) error
    Execute(ctx, action, attrs) (outputs, error)
    Close() error
}
```

### Wire Protocol

External providers speak JSON Lines — one JSON object per line, request/response matched by `id`. Four methods:

- `describe` → `{schema}` — returns the provider's self-description. Called during spawn, before `configure`, so tools like `orchard plan` can validate scenarios without opening connections.
- `configure` → `{}` — sets up connections, credentials, caches. Called once per scenario run.
- `execute` → `{outputs}` — performs one action with fully-resolved attribute values.
- `shutdown` → `{}` — graceful close; Orchard waits for the subprocess to exit (force-killed after a timeout).

The engine always evaluates HCL on the Orchard side. Providers see a flat map of `cty.Value`s for attributes and return a flat map of outputs — they never touch HCL themselves. This is what makes the multi-language story viable: no language needs its own HCL parser or evaluator.

### Schema Authoring

Provider schemas are written in HCL and shipped alongside the provider binary. The authoring form uses HCL's native type expressions (`string`, `list(number)`, `object({id=number})`) rather than a separate type DSL — so types round-trip 1:1 with the engine's own `cty` type system:

```hcl
provider "postgres" {
  version  = "1"
  protocol = "1"

  config {
    attribute "dsn" {
      type     = string
      required = true
    }
  }

  action "sql" {
    attribute "query" {
      type     = string
      required = true
    }
    output { type = any }
  }
}
```

On the wire the schema travels as JSON (parsed by any language). On disk it's HCL (parsed by Orchard only). The Go SDK can also synthesize the schema from struct tags instead of a `schema.hcl` file — builtins get this for free.

### Source Schemes

Scenarios declare their providers by source string:

| Scheme | Example | Resolution |
|--------|---------|------------|
| `builtin/<name>` | `builtin/postgres` | Factory registered at binary startup. |
| `exec:<path>` | `exec:./providers/stripe` | Path to a subprocess binary, cwd-relative or absolute. |

Additional schemes (remote registries, OCI-distributed binaries) are possible future extensions; the source string is parsed through a scheme switch that stays open for them.

### Local Names and Disambiguation

Providers are keyed by the scenario-local name the user chooses in `required_providers`, not by the provider's schema name:

```hcl
required_providers {
  db    = { source = "builtin/postgres" }
  cache = { source = "builtin/postgres" }   # same source, different alias
}

provider "db"    { dsn = var.primary_dsn }
provider "cache" { dsn = var.cache_dsn }
```

This lets two instances of the same provider coexist with independent config.

When a single registered provider offers an action type, scenarios write it bare (`action "sql" "x"`) and the engine resolves it unambiguously. When multiple providers expose the same action type, the action body picks one with the reserved `provider` attribute:

```hcl
action "sql" "insert_row" {
  provider = "db"
  query    = "INSERT INTO ..."
}

action "sql" "warm_cache" {
  provider = "cache"
  query    = "SELECT ..."
}
```

`provider` is an Orchard-reserved attribute (like `depends_on` / `timeout` / `retry` in Milestone 3). It's stripped from the action body before the provider's own schema validation runs, so providers never have to know about it.

### Plan-time Validation

`orchard plan` instantiates every declared provider for schemas only — no `Configure`, no connections — and runs two validation passes before rendering:

1. **Structural** (`Engine.ValidateGraph`). Walks every action body against its provider's declared attribute schema. Catches unknown attributes, missing required fields, and action types without a matching provider. HCL source coordinates land in the error message.
2. **Reference type-check** (`Engine.ValidateReferences`). Builds a typed eval context — `var.*` with real values, each `action.<type>.<name>` as a typed-but-unknown cty value based on the provider's declared output shape — and evaluates every scenario expression (action attributes, outputs, component inputs) against it. HCL's type-checker flags references that don't exist (`action.X.Y.typo`), attribute access on a primitive (`.status.foo` where status is a number), and mismatched types.

The same two passes also run at the start of `orchard run`, before any provider `Configure` opens a connection. A typo-riddled scenario fails fast without opening databases or spawning subprocesses.

Coverage caveats: actions with `output { type = any }` (today: `postgres.sql`) are typed as `cty.DynamicPseudoType`, which HCL allows any attribute access against — typos there remain runtime errors. Component outputs are also treated as dynamic; type-propagating through component output expressions is future work.

### Writing a Provider

**Builtin (Go, in-process):**

1. Create `pkg/provider/<name>/` with an embedded `schema.hcl` and a `Provider` struct implementing `Schema()`, `Configure()`, `Execute()`, `Close()`.
2. Add a `register.go` with `provider.RegisterBuiltin("builtin/<name>", New)` in `init()`.
3. Blank-import the package from `cmd/run.go` so the `init` fires at startup.

See `pkg/provider/postgres/` and `pkg/provider/http/` as references.

**External (any language, subprocess):**

1. Write a program that reads JSON Lines from stdin and writes JSON Lines to stdout. Handle four methods: `describe`, `configure`, `execute`, `shutdown`.
2. Return your schema from `describe` as a JSON object matching `pkg/protocol/schema.go`'s `ProviderSchema` shape.
3. Reserve stdout for protocol traffic; use stderr for diagnostics (Orchard streams it to the log).
4. Exit cleanly after responding to `shutdown`.

See `test/integration/testdata/echo/main.go` for a minimal reference (~100 lines of standard-library Go, zero Orchard imports).

---

## Action Types

The system supports multiple action types, each implemented by a provider. A scenario's `required_providers` block decides which ones are available for that run.

### SQL (builtin/postgres)

Execute arbitrary SQL against the configured PostgreSQL connection. Results from `RETURNING` clauses are captured as outputs.

```hcl
action "sql" "create_merchant" {
  query = <<-SQL
    INSERT INTO merchants (name, region)
    VALUES ('${var.name}', '${var.region}')
    RETURNING id, name, region
  SQL
}
```

### HTTP Request (builtin/http)

Make HTTP requests to APIs or services. The provider supports a `base_url` config for relative paths, `default_headers` merged across every request, and a request-level timeout. Responses expose `status`, `body`, and `headers` (first value per key) as outputs.

```hcl
provider "http" {
  base_url = var.api_base
  default_headers = {
    User-Agent = "orchard/1"
  }
}

action "request" "trigger_sync" {
  method = "POST"
  url    = "/sync"
  headers = {
    Content-Type = "application/json"
  }
  body = jsonencode({
    merchant_id = component.merchant.base.id
  })
}
```

### External Providers

Any action implemented by a subprocess provider. An external provider ships a binary plus (optionally) a `schema.hcl` and is referenced by its path:

```hcl
required_providers {
  stripe = {
    source = "exec:./providers/orchard-stripe"
  }
}

provider "stripe" {
  api_key = var.stripe_key
}

action "charge" "test_payment" {
  customer_id = component.customer.base.id
  amount      = 500
  currency    = "usd"
}
```

The action-type label (`"charge"`) matches an entry in the provider's schema. The full set of actions a scenario can use is the union of all declared providers' action names.

### Shell Commands (builtin/exec)

Run external programs and capture stdout, stderr, and exit code. Non-zero exit is an error by default (documented behaviour changes when retry / success-codes land).

```hcl
provider "exec" {
  dir = "/opt/myproj"
  env = { PATH = "/usr/local/bin:/usr/bin" }
}

action "run" "seed" {
  command = "./scripts/seed"
  args    = ["--env", "test"]
  env     = { DATABASE_URL = var.dsn }
  stdin   = file("fixtures.sql")
}

action "run" "reindex" {
  depends_on = [action.run.seed]
  command    = "./scripts/reindex"
  timeout    = "30s"
}
```

Outputs: `{stdout = string, stderr = string, exit_code = number}`. Provider-level `dir` and `env` act as defaults that per-action values override.

### Planned: Schema-Aware Database Operations

A typed database provider that validates operations against the actual database schema — checking that tables exist, columns exist, types match, and required fields are present.

```hcl
action "db.insert" "merchant" {
  table = "merchants"

  values = {
    name   = "Acme"
    region = "us"
  }

  returning = ["id", "name"]
}
```

---

## Execution Model

### Dependency Resolution

Dependencies are inferred automatically from expression references. When an action references `component.merchant.base.id`, Orchard knows that the merchant component must complete before that action can run. No explicit ordering is required for most cases.

For side-effect-only dependencies — where one action must run after another without reading any of its outputs — actions accept a `depends_on` list:

```hcl
action "request" "reindex" {
  depends_on = [action.sql.seed_data]
  method = "POST"
  url    = "/reindex"
}
```

Entries must be node-reference traversals (`action.X.Y`, `component.X.Y`). Inside a component, references scope to the component's internal actions.

### Action-Level Timeouts

Individual actions can set a wall-clock deadline:

```hcl
action "request" "slow_api" {
  timeout = "30s"
  method  = "POST"
  url     = "/slow-endpoint"
}
```

The engine wraps `provider.Execute` in `context.WithTimeout`. On breach the error names the action and the timeout so scenario authors see `action X exceeded timeout 30s` rather than a bare `context deadline exceeded`. Go duration syntax: `"500ms"`, `"30s"`, `"2m"`.

### Execution Phases

1. **Parse** — Load HCL files, structurally decode scenarios and components, resolve component source paths, merge variable defaults with CLI overrides, extract `required_providers` and evaluate each `provider` block against the variable context.
2. **Plan** — Build the dependency graph from expression references. Topologically sort nodes. Detect cycles. Register providers for schemas only (no `Configure`). Run `Engine.ValidateGraph` (structural: unknown attributes, missing required fields, unrouted action types) and `Engine.ValidateReferences` (typed eval context: reference typos, bad attribute access on primitives, mismatched types). Both passes attach HCL source coordinates to errors.
3. **Execute** — Configure every provider with its resolved config. Walk the topological order. For each node: build an `EvalContext` from resolved upstream outputs, evaluate the node's deferred expressions, coerce attrs to their declared types, dispatch to the appropriate provider, capture outputs.
4. **Output** — Evaluate and display scenario-level outputs.
5. **Close** — Close providers in reverse registration order. External subprocesses receive `shutdown` and are waited on (force-killed after a timeout if unresponsive).

### Determinism

Orchard aims for reproducible runs:

- **Deterministic execution order.** Kahn's topological sort emits siblings in lexicographic order of their node IDs, so same graph → same walk, run after run. Provider registration iterates `required_providers` in sorted order; scenario outputs and variables print alphabetically.
- **Deterministic close order.** Providers close in reverse registration order. Subprocess shutdown is bounded by a single deadline across the graceful RPC and the process wait — a hung provider cannot wedge orchard's exit.
- **Fail-fast on authoring collisions.** Duplicate variable names, action `(type, name)` pairs, component instance keys, output names, and provider blocks are rejected at parse time rather than silently overwriting.
- **Seeded random generators** for data generation (planned; individual providers responsible).
- **Stable IDs** where possible (planned; tied to Milestone 5's run-record model).
- **Explicit timestamp control** (planned; HCL `now()` and clock-freeze in Milestone 7).

---

## Lifecycle and Teardown

Lifecycle is a first-class concern, not an afterthought. Once Orchard can create state across databases, APIs, file systems, and external services, cleaning that state up becomes one of the hardest and most important problems.

### Run Records

Every scenario execution produces a **run record** containing:

- Run ID (unique identifier)
- Scenario name and version
- Input variables
- Timestamps (start, end)
- Status (success, failure, partial)
- Actions executed and their outputs
- Resource registry (what was created)
- Registered cleanup operations

This makes teardown concrete and inspectable: instead of "undo the scenario type," you teardown a specific run instance.

### Teardown Modes

**Immediate teardown** — for tests and ephemeral setups:

```bash
orchard run fraud_case --teardown
```

Execute the scenario, then automatically clean up everything it created.

**Deferred teardown** — for dev, QA, and demos:

```bash
orchard run support_case        # returns run_id: run_01H...
# ... use the environment ...
orchard teardown run_01H...     # clean up when done
```

**Convenience shortcuts:**

```bash
orchard teardown merchant_with_chargeback --latest
orchard teardown merchant_with_chargeback --all
```

### Resource Ownership

Actions register the resources they create into the run record. For example, a `db.insert` action registers the table and primary key of the inserted row. An `http` action that creates an API resource registers its URL and deletion endpoint.

Resources are classified by cleanup capability:

- **Managed** — Orchard knows exactly how to clean up (e.g., `db.insert` → `DELETE FROM table WHERE id = ?`)
- **Explicit cleanup** — user provides teardown logic via a `lifecycle` block
- **Unmanaged** — no automatic cleanup (e.g., sending an email, enqueuing a message)

### Lifecycle Blocks

Actions can declare explicit teardown logic:

```hcl
action "sql" "create_merchant" {
  query = <<-SQL
    INSERT INTO merchants (name) VALUES ('${var.name}') RETURNING id
  SQL

  lifecycle {
    teardown_query = <<-SQL
      DELETE FROM merchants WHERE id = ${action.sql.create_merchant.id}
    SQL
  }
}
```

### Failure Behavior

Scenarios can declare lifecycle policies:

```hcl
scenario "fraud_case" {
  lifecycle {
    on_failure = "rollback"    # undo partial state on failure
    on_success = "retain"      # keep state until explicit teardown
  }
}
```

### Reverse-Order Cleanup

Teardown executes in reverse dependency order. If a run created org → user → transaction → alert, teardown removes alert → transaction → user → org. The dependency graph that drives execution also drives cleanup.

### Isolation Strategies

The cleanest teardown is often destroying an isolation boundary rather than deleting individual resources:

- **Dedicated schema per run** — create `orchard_run_123` schema, teardown drops it
- **Tenant/namespace per run** — tag everything with a run ID, teardown removes the namespace
- **Individual resource cleanup** — delete rows/files/resources one by one (the fallback)

---

## Schema-Aware Database Provider

Orchard can introspect the user's database schema to provide a **typed database layer** that validates operations at plan time rather than failing at execution time with SQL errors.

### Schema Sources

**Runtime introspection** — connect to the database and inspect the schema dynamically. Always up to date, but requires a live connection.

**Snapshot (preferred for CI and tooling):**

```bash
orchard schema pull
```

Generates `.orchard/schema.json` containing tables, columns, types, nullability, defaults, primary keys, foreign keys, and enums. This snapshot is used for validation, autocomplete, and editor tooling.

### Validation

The schema-aware provider validates:

- Table exists
- Columns exist
- Value types match column types
- Required (non-nullable, no-default) fields are present
- Generated/identity columns are not manually specified
- `RETURNING` columns exist

### Layered Abstraction

The database interaction model provides three layers, from most flexible to most structured:

| Layer | Description | Example |
|-------|-------------|---------|
| **Raw SQL** | Maximum flexibility, no validation | `action "sql" "seed" { query = "..." }` |
| **Typed DB** | Schema-aware, validated operations | `action "db.insert" "merchant" { table = "merchants" ... }` |
| **Components** | Business-level abstractions | `component "merchant" "base" { source = "..." }` |

Users choose the appropriate layer for each situation. Raw SQL for complex queries and migrations. Typed DB for common CRUD. Components for reusable business-level state.

---

## CLI Design

```bash
# Execute a scenario
orchard run merchant_with_chargeback

# With variable overrides
orchard run merchant_with_chargeback \
  --var merchant_name=Acme \
  --var transaction_count=50

# Show execution plan without running
orchard plan merchant_with_chargeback

# List available scenarios
orchard list

# Teardown a specific run
orchard teardown <run-id>

# Pull database schema for validation
orchard schema pull

# Inspect a run
orchard inspect <run-id>

# List past runs
orchard list runs
```

### Configuration

Provider configuration lives in the scenario itself, evaluated against the variable context:

```hcl
variable "dsn" {
  default = "postgres://localhost:5432/orchard?sslmode=disable"
}

provider "postgres" {
  dsn = var.dsn
}
```

Runtime overrides use `--var`:

```bash
orchard run scenarios/demo.hcl --var dsn=postgres://prod-host/db
```

This replaces earlier flag/env-var precedence with a single mechanism — any provider attribute flows through `variable` blocks — so new providers don't need CLI-level configuration plumbing.

---

## Directory Structure

```
project/
  components/
    merchant.hcl
    transactions.hcl
    dispute.hcl
  scenarios/
    merchant_with_chargeback.hcl
    demo_env.hcl
    support_debug.hcl
  providers/                # External provider binaries (exec: sources)
    orchard-stripe
    orchard-intercom
  .orchard/
    schema.json             # Pulled database schema (future milestone)
```

---

## Design Principles

### Composable

Components are reusable and parameterized with clear input/output interfaces. A component that creates a merchant can be used in dozens of scenarios without duplication.

### Declarative-first

Structure is defined in HCL, not imperative scripts. The dependency graph is inferred from references. Users describe what they want, not how to orchestrate it step by step.

### Deterministic

Runs should be reproducible given the same inputs and database state. Randomness is seeded. Ordering is determined by the dependency graph, not insertion order.

### Observable

Clear execution logs showing what ran, what was created, what outputs were produced, and what failed. The `plan` command shows the execution graph without running anything.

### Extensible — in any language

New action types live behind a single `Provider` interface. Builtins are Go packages compiled into the binary; external providers are subprocesses speaking JSON Lines on stdio and can be written in any language with JSON support. The engine evaluates HCL on its own side and hands providers a flat attribute map, so external authors never need an HCL parser or evaluator.

### Schema-driven, validated early

Providers advertise their schemas; the engine validates scenario action bodies against those schemas at plan time. Typos and missing required attributes surface with source coordinates before any side effects run.

### Escape hatches always available

Raw SQL and command execution are first-class. The tool should never force users into a higher abstraction when they need direct control.

---

## Non-Goals (v1)

- Full workflow engine with complex control flow (loops, branches, conditionals)
- Infrastructure provisioning (use Terraform for that)
- Complete ORM replacement
- Cross-database parity (Postgres first, others later)
- Remote component registry
- Visual UI / editor plugins

---

## Roadmap

### Milestone 1: Foundation (MVP) — COMPLETE

The core execution pipeline: parse HCL scenarios and components, build a dependency graph, execute actions in topological order against PostgreSQL, propagate outputs between nodes.

| Feature | Status |
|---------|--------|
| Go project structure and module setup | Done |
| CLI skeleton (`orchard run`, `orchard plan`, `orchard list`) | Done |
| HCL parser with two-pass decode (deferred expressions) | Done |
| Schema structs: Scenario, Component, Action, Variable, Output | Done |
| Component source resolution from file paths | Done |
| Variable resolution (defaults + `--var` CLI overrides) | Done |
| Dependency graph construction from `Expression.Variables()` traversals | Done |
| Topological sort (Kahn's algorithm) with cycle detection | Done |
| Execution engine with EvalContext builder | Done |
| Component-scoped action namespacing and output resolution | Done |
| Component input evaluation in scenario scope | Done |
| SQL action provider (PostgreSQL via pgx) | Done |
| `RETURNING` clause output capture as cty values | Done |
| `orchard plan` — parse, graph, print execution order | Done |
| `orchard list` — scan directories for scenario files | Done |
| Provider registry and interface | Done |
| DSN resolution via CLI flag / env var / default | Done, then superseded in Milestone 2 by scenario-declared `provider "postgres" { dsn = var.dsn }` |
| Example scenarios (basic + with_component) | Done, then moved into `test/integration/testdata/` |
| Unit tests for parser, graph, and engine | Done |

**What works today:**

```bash
# Plan — parses, builds the DAG, validates every action body against its
# provider's declared schema. No database or network access.
orchard plan path/to/scenario.hcl

# With variable overrides (typed against the scenario's variable declarations).
orchard plan path/to/scenario.hcl --var merchant_name=Override --var count=50

# Execute against Postgres. Connection info lives in the scenario's
# `provider "postgres" { dsn = var.dsn }` block; --var feeds the DSN in.
orchard run path/to/scenario.hcl --var dsn=postgres://...

# List scenario files under a directory.
orchard list path/to/dir/
```

### Milestone 2: Provider Plugin System — COMPLETE

Rebuild the action layer around a language-agnostic plugin model so third parties can extend Orchard without forking the binary. Borrowed from Terraform's provider model, trimmed to fit Orchard's synchronous workload: JSON Lines over stdio instead of gRPC, HCL for schema authoring, `cty` types as the universal type DSL.

| Feature | Status |
|---------|--------|
| `pkg/protocol` — envelope, schema types, HCL parser, cty↔JSON codec | Done |
| `pkg/provider.Provider` unified interface | Done |
| Builtin factory registry (`builtin/<name>` sources) | Done |
| External subprocess adapter (`exec:<path>` sources) | Done |
| `describe` / `configure` / `execute` / `shutdown` wire protocol | Done |
| `required_providers` + `provider "<name>" {}` HCL blocks | Done |
| Plan-time schema validation (`orchard plan` type-checks action bodies) | Done |
| Schema-driven HCL evaluation (engine evaluates, providers receive flat attrs) | Done |
| Postgres builtin ported onto the new interface | Done |
| HTTP builtin (status/body/headers outputs) | Done |
| Cross-provider integration coverage (FK chain + external echo) | Done |
| Typed variables (`type = ...`) with default/override coercion | Done |
| Strict component inputs (reject undeclared, require required) | Done |
| Deterministic execution and plan ordering | Done |
| Presentation layer separated from engine (Reporter interface) | Done |
| Context-aware external providers (Execute/Close bounded by ctx) | Done |
| Duplicate scenario element detection (variables, actions, outputs, components) | Done |
| Alias-based provider identity (`required_providers { db = {...} }`) | Done |
| Multi-provider disambiguation via `provider = "<alias>"` on actions | Done |
| Plan-time validation of scenario output references | Done |
| Plan-time validation of component inputs (static keys + required check) | Done |

### Milestone 3: Action Ergonomics and Routing — COMPLETE

Fill in the remaining action-layer features that were in the original Milestone 2 before the plugin refactor, plus the provider-routing work that the current "unique action name across providers" model forecloses.

| Feature | Status |
|---------|--------|
| `depends_on` explicit dependency support | Done |
| Action-level `timeout = "..."` | Done |
| Shell command builtin (`builtin/exec`, `action "run"`) | Done |
| Action `retry { ... }` block (constant / linear / exponential backoff, max_delay cap) | Done |
| Plan-time reference type-checking (output types → typed eval context) | Done |
| Dotted action types (`action "db.insert"`) | Deferred — see below |

**Reserved action attributes and blocks.** Four items now live under orchard's reservation namespace, stripped from action bodies before provider schema validation runs: `provider = "<alias>"`, `depends_on = [...]`, `timeout = "..."`, and the `retry { ... }` block. The reservation mechanism sits in one place (`internal/graph/graph.go`), so any future addition is a localised change.

**Plan-time reference type-checking.** `orchard plan` (and `orchard run` before Configure) now builds a typed eval context — `var.*` with resolved values, each `action.<type>.<name>` as `cty.UnknownVal` of the provider's declared output type — and walks every scenario expression against it. Typos like `action.request.fetch.bdy` surface as HCL "Unsupported attribute" errors with source coordinates, not at runtime. Actions whose provider declares `output { type = any }` (postgres.sql today) remain dynamic; those typos stay runtime errors until the provider narrows its schema.

**Routing today.** The engine routes actions by the scenario's local provider alias. `required_providers { db = { source = "builtin/postgres" } }` registers the postgres provider under the name `db`; `provider "db" {}` configures it; and an action body can pick it explicitly with `provider = "db"`. When only one registered provider offers a given action type, the action body can omit `provider =` and the engine resolves it automatically. When two or more providers offer the same action type, the engine returns a clear error listing the candidates and pointing at the `provider = "<alias>"` escape.

**Open — dotted action types.** The design doc's earlier `action "db.insert" "merchant"` example implies a dotted action-type label with a corresponding `action.db.insert.merchant.id` traversal. HCL attribute traversals don't naturally parse that — `action.db.insert.merchant.id` is five attribute accesses, not "action type db.insert / name merchant / field id". The options are (a) pivot to three-label actions `action "<provider>" "<action>" "<name>"` with a scenario migration, or (b) add a special parse path for dotted action-type labels. Deferred until the need is concrete; today's alias-based routing covers the plugin-ecosystem use cases that motivated the concern.

### Milestone 4: Schema-Aware Database Provider

Introspect PostgreSQL schemas and provide a typed database layer that validates operations before execution.

| Feature | Status |
|---------|--------|
| `orchard schema pull` — introspect Postgres and generate schema snapshot | Not started |
| `.orchard/schema.json` schema model (tables, columns, types, keys, constraints) | Not started |
| `action "db.insert"` — schema-validated inserts | Not started |
| `action "db.update"` — schema-validated updates | Not started |
| `action "db.delete"` — schema-validated deletes | Not started |
| Column type validation | Not started |
| Required field enforcement | Not started |
| Default/generated column handling | Not started |
| `RETURNING` field validation | Not started |
| Foreign key reference hints | Not started |

### Milestone 5: Lifecycle and Teardown

Make cleanup a first-class operation so that scenarios can be torn down cleanly, whether immediately after a test or hours later in a dev environment.

| Feature | Status |
|---------|--------|
| Run ID generation and run record persistence | Not started |
| Resource registry (track what each action creates) | Not started |
| `orchard teardown <run-id>` — destroy a specific run's resources | Not started |
| Reverse-order cleanup based on dependency graph | Not started |
| Managed cleanup for `db.insert` / `db.update` actions | Not started |
| `lifecycle` blocks on actions for explicit teardown logic | Not started |
| `lifecycle` blocks on scenarios (`on_failure`, `on_success` policies) | Not started |
| Rollback on partial failure | Not started |
| `orchard list runs` — list past runs | Not started |
| `orchard inspect <run-id>` — show run details | Not started |
| `orchard teardown <scenario> --latest` convenience | Not started |
| Run metadata tagging on created resources | Not started |

### Milestone 6: Robustness and Ergonomics

Harden the tool for real-world use and improve the authoring experience.

| Feature | Status |
|---------|--------|
| Styled terminal output (lipgloss, TTY vs Plain reporters) | Done |
| `orchard plan` as a DAG-shaped dependency tree | Done |
| Engine-Reporter split so CLI presentation lives outside the engine | Done |
| `--no-color` / `NO_COLOR` respect | Done |
| Scenario and component `description` and `tags` metadata | Not started |
| Improved error messages with HCL source locations | Partial — parser/plan errors carry HCL coordinates; runtime errors don't yet |
| Dry-run mode (plan with expression evaluation but no execution) | Not started |
| Execution logging with structured output | Not started |
| Multiple scenarios per file support | Not started |
| HCL function library (`sqlstring`, `uuid`, `timestamp`, etc.) | Not started |
| Parallel execution of independent nodes | Not started |
| Transaction wrapping for atomic scenario execution | Not started |
| Configuration file (`.orchard/config.hcl` or similar) | Not started |

### Milestone 7: Advanced Features (Future)

| Feature | Status |
|---------|--------|
| Schema-driven factory helpers (auto-generate required fields by type) | Not started |
| Foreign-key-aware record generation | Not started |
| Scenario composition (scenario referencing other scenarios) | Not started |
| Remote component registry / versioned component packages | Not started |
| Editor tooling and autocomplete | Not started |
| Visual execution graph rendering | Not started |
| Test/assertion integration (`assert.sql`, `assert.http`) | Not started |
| Embedded scripting (Starlark or Lua) for custom generators | Not started |
| Isolation strategies (per-run schema, namespace tagging) | Not started |
| Clock freezing / deterministic timestamp control | Not started |
| SQLite support | Not started |
| MySQL support | Not started |

---

## Architecture

### Current Implementation

```
main.go                         Entry point
cmd/
  root.go                       Cobra root, global --var flag
  run.go                        orchard run — parse → graph → validate → configure → execute
  plan.go                       orchard plan — parse → graph → schema-validate, print DAG
  list.go                       orchard list — scan for scenario files
  registry.go                   registerProviders + buildRegistry helpers (shared by run/plan)

internal/
  parser/
    schema.go                   HCL schema structs: Scenario, Component, RequiredProviders,
                                ProviderConfig, Variable, Action, Output
    parser.go                   LoadScenario: parse, decode, extract required_providers,
                                evaluate provider configs, resolve components, merge vars

  graph/
    graph.go                    Build DAG from Expression.Variables() traversals;
                                component-input expression deps propagate to internal actions
    topo.go                     Kahn's algorithm topological sort

  engine/
    engine.go                   Walk topo order, resolve compVars per component,
                                dispatch via pkg/provider.Provider. Reporter
                                interface (RunStarted/NodeStarted/NodeCompleted/
                                NodeFailed/RunCompleted) decouples the engine
                                from presentation.
    evalattrs.go                Schema-driven HCL body → map[string]cty.Value with
                                coercion to declared types and defaults from schema
    validate.go                 Engine.ValidateGraph — structural schema check, no eval
    validate_refs.go            Engine.ValidateReferences — typed eval context,
                                walks scenario expressions and catches reference typos
                                + type mismatches against provider output schemas
    context.go                  Build nested cty.ObjectVal EvalContext from resolved outputs

  cli/                          CLI-only helpers, kept out of engine/
    render/                     Lipgloss semantic styles (Success/Error/Muted/Label/
                                Kind/Ref), color palette in one place
    reporter/                   PlainReporter + TTYReporter implementations plus
                                Select(Options) that picks via isatty/NO_COLOR/--no-color
    planview/                   Renders a ParsedScenario + Graph as a dependency-
                                tree plan with Terraform-style block headers

pkg/ (public SDK surface)
  protocol/
    envelope.go                 Request/Response/Error + JSON Lines framing helpers
    methods.go                  describe / configure / execute / shutdown param/result types
    schema.go                   ProviderSchema, ActionSchema, AttrSchema + Validate,
                                CtyType (textual type → cty.Type via hcl/ext/typeexpr)
    schema_hcl.go               Authoring-form parser: provider/config/action/attribute/output blocks
    ctyjson.go                  cty.Value ↔ json.RawMessage helpers using go-cty/cty/json

  provider/
    provider.go                 Provider interface (Schema/Configure/Execute/Close) + Registry
    builtins.go                 Source-string → factory-function registry for "builtin/*"
    postgres/                   builtin/postgres — raw SQL against pgx pool
      schema.hcl                Embedded via //go:embed
      provider.go, sql.go, cty.go, register.go
    http/                       builtin/http — net/http-backed request action
      schema.hcl                Embedded via //go:embed
      provider.go, register.go
    exec/                       builtin/exec — os/exec-backed run action
      schema.hcl                Embedded via //go:embed
      provider.go, register.go
    external/                   exec:<path> subprocess adapter
      client.go                 JSON Lines transport with atomic id counter + roundtrip mutex
      external.go               Spawn + lifecycle (describe handshake, stderr drain, shutdown)

test/
  integration/                  Separate go module with testcontainers-go Postgres
    harness_test.go             Container bootstrap, goose migrations, echo-fixture build
    scenario_test.go            Full-pipeline tests: basic, with-component, echo, http,
                                combined (cross-provider + FK diamond)
    http_fixture_test.go        Tiny ServeHTTP stub with per-path callbacks
    testdata/
      scenarios/                basic, with_component, echo, http, combined
      components/merchant.hcl   Shared component used by with_component
      echo/main.go              External echo provider (re-built by TestMain)
    migrations/
      00001_domain.sql          merchants, products
      00002_reviews.sql         product_reviews (FK both parents — enables diamond test)
```

### Key Design Decisions

**Two-pass HCL decode** — Structural decode at parse time with `hcl.Expression` for deferred fields. Expression evaluation at execution time with a populated `EvalContext`. This is the foundation that makes dependency graph construction and lazy evaluation work together.

**Component-scoped namespacing** — A component's internal actions get node IDs like `component.merchant.base.action.sql.create`. At execution time, the engine remaps these to bare `action.sql.create` references within the component's scope, so component authors write `action.sql.create.id` without knowing their component's instance name. The `var.*` namespace visible inside a component is the component's resolved inputs (merged with its declared variable defaults), not the scenario's — scoping is enforced in both directions.

**cty type system as the universal type DSL** — All values flowing through the system use `cty` (from HCL). Provider schemas declare types in HCL's own type-expression syntax (`string`, `map(number)`, `object({id=number})`); on the wire these travel as compact strings (`"object({id=number})"`), parseable by any JSON-capable language without an HCL library. This is what makes the multi-language provider story work without forcing external authors to ship an HCL parser.

**Schema-driven evaluation at the boundary** — The engine evaluates action bodies against each provider's declared schema, coerces values to their declared types, applies schema defaults, and passes providers a flat `map[string]cty.Value`. Providers never see `hcl.Body` or `hcl.EvalContext`. This localises all HCL-specific concerns to the engine, which is exactly what the JSON-over-stdio adapter needs to stay a pure byte-mover.

**JSON Lines over stdio for external providers** — Not gRPC. Not custom binary. The smallest transport that supports multi-language providers, matching what LSP, DAP, and MCP all converged on. Borrowed from Terraform's plugin model (two-phase describe/configure handshake, per-provider schema advertisement) without the protobuf toolchain cost.

**Plan-time schema validation** — `orchard plan` instantiates providers for schemas only and runs structural validation (unknown attrs, missing required fields, unrouted action types) against every action body before any `Configure` runs. Catches typos without opening database connections or making network calls. A second pass, `Engine.ValidateReferences`, builds a typed eval context (variables resolved, node outputs as typed-but-unknown cty values) and walks every scenario expression — HCL's own type-checker then flags reference typos, attribute access on the wrong type, and undeclared variables. Both passes run at the start of `orchard run` too, so even if the user skipped `plan`, a typo-riddled scenario fails before Configure opens anything.

**Provider sources are scheme-prefixed strings** — `builtin/<name>` for compiled-in, `exec:<path>` for subprocess. The scheme switch lives in `cmd/registry.go` and stays open for future schemes (remote registries, OCI-distributed binaries) without touching the engine or protocol.

**Typed variables, coerced at the boundary** — `variable "foo" { type = number }` is enforced. Defaults coerce at parse time, CLI overrides HCL-parse against the declared type, and component inputs coerce when crossing the module boundary. A typo like `inputs = { naem = var.x }` fails with "undeclared input \"naem\" on component c" before anything runs. Runtime receives well-typed values.

**Context-bounded subprocess lifecycle** — `external.Client.Roundtrip` takes a `context.Context`; both the write and read run in one goroutine selected against `ctx.Done`. On cancellation the client poisons itself — subsequent calls fail fast — so there's never more than one goroutine reading the pipe. `Provider.Close` wraps the shutdown RPC and the process wait in a single `context.WithTimeout(shutdownTimeout)`, capping total teardown time regardless of which half hangs.

**Presentation outside the engine** — `engine.Execute` emits progress through a tiny `Reporter` interface with a `NopReporter` default. `cmd/` installs a stdout reporter; integration tests get silence for free. The engine has no direct opinion on stdout, logging, or formatting.

### Dependencies

| Dependency | Purpose |
|------------|---------|
| `github.com/hashicorp/hcl/v2` | HCL parsing, expression evaluation, `ext/typeexpr` for type-expression ↔ `cty.Type` |
| `github.com/zclconf/go-cty` | Type system for values flowing through the engine; `cty/json` for wire encoding |
| `github.com/jackc/pgx/v5` | PostgreSQL driver and connection pooling (used by `builtin/postgres`) |
| `github.com/spf13/cobra` | CLI framework |
| Go standard library (`net/http`, `os/exec`, `encoding/json`) | `builtin/http` and the external-provider subprocess adapter |

No other runtime dependencies. The tool is a single static binary; external providers are separate binaries the user builds and ships alongside their scenarios.

The integration test suite (`test/integration/`) is a separate Go module and pulls in additional dependencies (`testcontainers-go`, `pressly/goose`, `stretchr/testify`) so the production binary stays lean.

---

## Positioning

Orchard is not a fixture library. It is not a migration tool. It is not a workflow engine.

It is a tool for defining **reproducible application state as code** — composing realistic system states using scenarios and reusable components.

For teams where "seed the database" actually means "recreate a believable system state across multiple tables, services, and side effects," Orchard provides the structure, composability, and lifecycle management that ad-hoc scripts cannot.

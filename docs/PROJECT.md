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
  variable "merchant_name" {
    default = "Acme Corp"
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

### Two-Pass HCL Evaluation

This is the core architectural decision. HCL expressions cannot be fully evaluated at parse time because their values depend on execution results (e.g., a database-generated ID). Orchard uses a two-pass approach:

**Pass 1 — Structural decode (parse time):** Decode the HCL block structure (scenario names, component labels, action types) but capture expression-valued fields as unevaluated `hcl.Expression` handles. Use `Expression.Variables()` to extract dependency references for the graph — this works without any values being resolved.

**Pass 2 — Expression evaluation (execution time):** For each node in topological order, build an `hcl.EvalContext` populated with all resolved upstream outputs and variables, then call `Expression.Value(ctx)` to evaluate deferred expressions. This preserves HCL's full expression language — string interpolation, functions, type coercion — while enabling dependency-driven execution.

---

## Action Types

The system supports multiple action types, each implemented as a **provider**. Providers are registered by name and receive the action's HCL body plus an evaluation context at execution time.

### SQL

Execute arbitrary SQL against the connected database. Results from `RETURNING` clauses are captured as outputs.

```hcl
action "sql" "create_merchant" {
  query = <<-SQL
    INSERT INTO merchants (name, region)
    VALUES ('${var.name}', '${var.region}')
    RETURNING id, name, region
  SQL
}
```

### Command Execution

Run external commands or scripts. Parameters are passed to the command.

```hcl
action "cmd" "generate_data" {
  command = "./scripts/generate_transactions"

  params = {
    merchant_id = component.merchant.base.id
    count       = var.transaction_count
  }
}
```

### HTTP

Make HTTP requests to APIs or services.

```hcl
action "http" "trigger_sync" {
  method = "POST"
  url    = "${var.api_base}/sync"

  body = {
    merchant_id = component.merchant.base.id
  }
}
```

### Schema-Aware Database Operations

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

An explicit `depends_on` escape hatch is available for side-effect dependencies that don't involve data references:

```hcl
action "http" "reindex" {
  depends_on = [action.sql.seed_data]
  ...
}
```

### Execution Phases

1. **Parse** — Load HCL files, structurally decode scenarios and components, resolve component source paths, merge variable defaults with CLI overrides.
2. **Plan** — Build the dependency graph from expression references. Topologically sort nodes. Detect cycles. Validate that all referenced nodes exist.
3. **Execute** — Walk the topological order. For each node: build an EvalContext from resolved upstream outputs, evaluate the node's deferred expressions, dispatch to the appropriate provider, capture outputs.
4. **Output** — Evaluate and display scenario-level outputs.

### Determinism

Orchard aims for reproducible runs:

- Seeded random generators for deterministic data generation
- Stable IDs where possible
- Explicit timestamp control
- Controllable execution ordering via the dependency graph

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

Database connection is resolved in priority order:

1. `--dsn` CLI flag
2. `ORCHARD_DATABASE_URL` environment variable
3. `ORCHARD_DSN` environment variable
4. Default: `postgres://localhost:5432/orchard?sslmode=disable`

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
  .orchard/
    schema.json
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

### Extensible

New action types (providers) can be added without changing the core engine. The provider interface is simple: receive an HCL body and eval context, return outputs.

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
| Configuration: DSN resolution (flag / env / default) | Done |
| Example scenarios (basic + with_component) | Done |
| Unit tests for parser, graph, and engine | Done |

**What works today:**

```bash
orchard plan examples/basic/scenario.hcl              # prints DAG
orchard plan examples/basic/scenario.hcl --var merchant_name=Override
orchard list examples/                                 # finds scenarios
orchard run examples/basic/scenario.hcl --dsn postgres://...  # executes against Postgres
```

### Milestone 2: Action Providers

Expand the action type system beyond raw SQL to support the full range of side effects needed for realistic environment setup.

| Feature | Status |
|---------|--------|
| Command execution provider (`action "cmd"`) | Not started |
| HTTP request provider (`action "http"`) | Not started |
| Provider-specific HCL schema validation | Not started |
| Action output type coercion and validation | Not started |
| `depends_on` explicit dependency support | Not started |
| Action timeout support | Not started |
| Action retry support | Not started |

### Milestone 3: Schema-Aware Database Provider

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

### Milestone 4: Lifecycle and Teardown

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

### Milestone 5: Robustness and Ergonomics

Harden the tool for real-world use and improve the authoring experience.

| Feature | Status |
|---------|--------|
| Scenario and component `description` and `tags` metadata | Not started |
| Improved error messages with HCL source locations | Not started |
| Dry-run mode (plan with expression evaluation but no execution) | Not started |
| Execution logging with structured output | Not started |
| Multiple scenarios per file support | Not started |
| Scenario-level variables with type validation | Not started |
| HCL function library (`sqlstring`, `uuid`, `timestamp`, etc.) | Not started |
| Parallel execution of independent nodes | Not started |
| Transaction wrapping for atomic scenario execution | Not started |
| Configuration file (`.orchard/config.hcl` or similar) | Not started |

### Milestone 6: Advanced Features (Future)

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
  root.go                       Cobra root command, global flags
  run.go                        orchard run — parse → graph → engine → output
  plan.go                       orchard plan — parse → graph → print
  list.go                       orchard list — scan for scenario files

internal/
  config/config.go              DSN resolution (flag → env → default)

  parser/
    schema.go                   HCL schema structs (gohcl-tagged)
    parser.go                   LoadScenario: parse, decode, resolve components, merge vars

  graph/
    graph.go                    Build DAG from Expression.Variables() traversals
    topo.go                     Kahn's algorithm topological sort

  engine/
    engine.go                   Execute nodes in topo order, dispatch to providers
    context.go                  Build nested cty.ObjectVal EvalContext from resolved outputs

  provider/
    provider.go                 Provider interface
    registry.go                 Provider registry by action type name
    sql/sql.go                  PostgreSQL provider via pgx (query + RETURNING capture)
```

### Key Design Decisions

**Two-pass HCL decode** — Structural decode at parse time with `hcl.Expression` for deferred fields. Expression evaluation at execution time with a populated `EvalContext`. This is the foundation that makes dependency graph construction and lazy evaluation work together.

**Component-scoped namespacing** — A component's internal actions get node IDs like `component.merchant.base.action.sql.create`. At execution time, the engine remaps these to bare `action.sql.create` references within the component's scope, so component authors write `action.sql.create.id` without knowing their component's instance name.

**cty type system** — All values flowing through the system use the `cty` type system (from HCL). This provides type safety, null handling, and seamless integration with HCL expression evaluation. Provider outputs are converted from Go/database types to `cty.Value`.

**Provider interface** — Providers receive the raw `hcl.Body` and an `EvalContext`, giving them full control over how to decode their action-specific attributes. This means adding a new action type is a matter of implementing one interface method.

### Dependencies

| Dependency | Purpose |
|------------|---------|
| `github.com/hashicorp/hcl/v2` | HCL parsing, expression evaluation, gohcl structural decode |
| `github.com/zclconf/go-cty` | Type system for values flowing through the engine |
| `github.com/jackc/pgx/v5` | PostgreSQL driver and connection pooling |
| `github.com/spf13/cobra` | CLI framework |

No other runtime dependencies. The tool is a single static binary.

---

## Positioning

Orchard is not a fixture library. It is not a migration tool. It is not a workflow engine.

It is a tool for defining **reproducible application state as code** — composing realistic system states using scenarios and reusable components.

For teams where "seed the database" actually means "recreate a believable system state across multiple tables, services, and side effects," Orchard provides the structure, composability, and lifecycle management that ad-hoc scripts cannot.

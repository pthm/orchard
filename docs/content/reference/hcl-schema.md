---
title: HCL schema reference
linkTitle: HCL schema
weight: 20
---

Every block and attribute Orchard's parser accepts. The two authoring contexts
are **scenario files** and **component files**.

## Scenario files

A scenario file contains exactly one `scenario` block.

### `scenario`

```hcl
scenario "<name>" {
  description = "..."           # optional
  tags        = ["a", "b"]      # optional
  seed        = 42              # optional

  required_providers { ... }
  variable "<name>"  { ... }
  provider "<name>"  { ... }
  component "<type>" "<name>" { ... }
  action "<type>" "<name>"    { ... }
  output "<name>"    { ... }
}
```

A single HCL file may contain **multiple scenario blocks**. When it does,
`orchard run` and `orchard plan` require `--scenario <name>` to pick one.
Single-scenario files work without the flag.

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `description` | `string` | no | Free-text summary. Shown by `orchard list`. |
| `tags` | `list(string)` | no | Categorisation labels. Shown by `orchard list` in violet brackets. |
| `seed` | `number` | no | Seed for random functions, a literal non-negative integer. `--seed` overrides it. Without either, each run picks a random seed. See [random values and seeds](../expressions/#random-values-and-seeds). |

| Block | Cardinality | Purpose |
|-------|-------------|---------|
| `required_providers` | 0..1 | Declare which providers the scenario uses. |
| `variable` | 0..n | Scenario inputs. |
| `provider` | 0..n | Provider configurations. |
| `component` | 0..n | Instances of reusable components. |
| `action` | 0..n | Executable units. |
| `output` | 0..n | Values exposed to the caller. |

### `required_providers`

```hcl
required_providers {
  postgres = { source = "builtin/postgres" }
  stripe   = { source = "exec:./bin/orchard-stripe" }
}
```

A flat attribute set, one entry per provider. Each entry is an object with:

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `source` | `string` | yes | Provider source: `builtin/<name>` or `exec:<path>`. |

### `variable`

```hcl
variable "dsn" {
  type        = string
  default     = "postgres://localhost/dev"
  description = "Database connection string"
}
```

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `type` | type expression | no | Declared type. Defaults to dynamic (`any`) if omitted. |
| `default` | any | no | Default value. Used when no CLI override is given. |
| `description` | `string` | no | Free-text documentation. |
| `sensitive` | `bool` | no | Keep the value out of the run record. Defaults to `false`. |

Variables without a default **must** be supplied via `--var` at runtime.

A `sensitive` variable is stored in the run record as a hash of its value
rather than the value, so a DSN or API key doesn't end up in a file next to the
scenario. `orchard inspect` shows it as `(sensitive)`. To tear the run down,
pass the value again with `--var`; teardown checks it against the hash. A
sensitive variable with a default (`default = env("DB_PASSWORD")`) needs no
`--var` if the default still gives the run's value.

A component variable counts as sensitive, and is left out of the record too,
when it is declared `sensitive = true` or its input expression reads a
sensitive scenario variable (`inputs = { dsn = var.dsn }`).

Wherever else the value turns up (an action's outputs, such as a command
echoing it; a typed insert's undo step; a scenario output; an error message),
`orchard run` and `orchard teardown` replace it with `«sensitive:<name>»` in
what they print and record. Teardown puts the value back before using recorded
outputs and undo steps, so a row inserted with the secret is still found and
deleted. Matching is by value, so `run` refuses a sensitive string shorter than
4 characters: it would match unrelated text. Only strings are matched; a
sensitive number or bool is kept out of the variables in the record but not
out of outputs.

### `provider`

```hcl
provider "postgres" {
  dsn = var.dsn
}
```

The label selects which provider in `required_providers` is being configured.
The body is provider-specific — see the [providers reference](/providers/).

Provider configuration is evaluated with variables in scope; it cannot
reference action or component outputs.

### `component`

```hcl
component "merchant" "acme" {
  source = "../components/merchant.hcl"
  inputs = {
    name = "Acme Corp"
  }
}
```

| Label | Purpose |
|-------|---------|
| First | Component type (must match `component "<type>" { }` in the source file). |
| Second | Instance name, unique within the scenario. |

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `source` | `string` | yes | Path to the component file. Resolved relative to this scenario file. |
| `inputs` | object | — | Values for the component's declared variables. Required if the component has any required variables. |
| `count` / `for_each` | number / collection | — | Create several instances. See [Repetition](/docs/concepts/wiring/#repetition). |

### `action`

```hcl
action "postgres_query" "create_merchant" {
  query = "INSERT INTO ..."
}
```

| Label | Purpose |
|-------|---------|
| First | Action type (must match an action declared by some provider). |
| Second | Action name, unique within the scenario or component. |

Body attributes are defined by the provider's schema. In addition, Orchard
reserves a few attributes for engine-level behavior:

| Attribute | Type | Description |
|-----------|------|-------------|
| `provider` | `string` | Explicit provider alias. Required only when two providers offer the same action type. |
| `depends_on` | `list(reference)` | Explicit dependency edges. Each entry is a reference to another action or component node. |
| `timeout` | `string` | Go-style duration (e.g. `"30s"`, `"5m"`). Wraps the provider's `Execute` call. |
| `count` | `number` | Create this many instances. See [Repetition](/docs/concepts/wiring/#repetition). Scenario-level actions only. |
| `for_each` | map, object, or list/set of strings | One instance per element. Scenario-level actions only. |
| `retry` | block | Retry policy. See below. |
| `lifecycle` | block | Teardown configuration. See below. |

#### `retry` block

```hcl
action "http_request" "flaky" {
  url = "https://example.com"
  retry {
    max_attempts = 5
    delay        = "1s"
    backoff      = "exponential"
    max_delay    = "30s"
  }
}
```

| Attribute | Type | Description |
|-----------|------|-------------|
| `max_attempts` | `number` | Maximum number of attempts including the first. |
| `delay` | `string` | Initial delay between attempts, as a Go duration. |
| `backoff` | `string` | `"constant"` or `"exponential"`. |
| `max_delay` | `string` | Cap for exponential backoff. |

#### `lifecycle` block

Declares a teardown operation for the action. Read by `orchard teardown <record>`
when walking the graph in reverse.

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

The `teardown` block's body uses the **same schema as the outer action's
provider**. A sql action's teardown declares `query`; an http action's declares
`method` + `url`; an exec action's declares `command` + `args`.

Inside the `teardown` body, three reference namespaces are in scope:

- `var.<name>` — scenario variables (from the record). Inside a component,
  the component's variables, as the run resolved them (also from the record).
- `self.<field>` — this action's own recorded outputs. Use it to target the
  resources this action created, without restating the node name.
- `action.<type>.<name>.<field>` — any other action's recorded outputs, for
  teardowns that reference upstream state. Inside a component, only the
  component's own actions.

See [lifecycle](/docs/concepts/lifecycle/) for authoring guidance and the
[`self` namespace](../expressions/#self-teardown-only) under expressions.

### `output`

```hcl
output "merchant_id" {
  value = action.postgres_query.create_merchant.id
}
```

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `value` | any expression | yes | Expression to evaluate after the scenario completes. |

## Component files

A component file contains exactly one `component` block.

### `component`

```hcl
component "merchant" {
  description = "..."           # optional
  tags        = ["seed"]        # optional

  variable "<name>" { ... }
  action "<type>" "<name>" { ... }
  output "<name>"   { ... }
}
```

The label is the component's type. Component files may not declare
`required_providers` or `provider` blocks — components inherit providers from
the enclosing scenario.

| Attribute | Type | Required | Description |
|-----------|------|----------|-------------|
| `description` | `string` | no | Free-text summary. |
| `tags` | `list(string)` | no | Categorisation labels. |

Inside a component, `variable`, `action`, and `output` have the same schema as
in a scenario.

Components reference their own variables as `var.<name>`, their own actions as
`action.<type>.<name>.<output>`, and cannot reach scenario-level actions or
components.

## Evaluation order

1. **Parse** — decode scenario and component files structurally. Action bodies
   and output expressions are captured but not evaluated.
2. **Resolve variables** — apply defaults and CLI overrides.
3. **Evaluate provider configs** — with variables in scope.
4. **Build graph** — derive edges from `expr.Variables()` traversals plus
   `depends_on` lists.
5. **Topological sort**, then execute each node once its dependencies have
   completed (up to `orchard run --parallelism` at a time):
   - For an action: evaluate its body, call provider `Execute`, record outputs.
   - For a component: resolve inputs, execute internal actions, resolve outputs.
6. **Evaluate scenario outputs** with all node results in scope.

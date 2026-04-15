---
title: HCL schema
weight: 20
---

# HCL schema reference

Every block and attribute Orchard's parser accepts. The two authoring contexts
are **scenario files** and **component files**.

## Scenario files

A scenario file contains exactly one `scenario` block.

### `scenario`

```hcl
scenario "<name>" {
  required_providers { ... }
  variable "<name>"  { ... }
  provider "<name>"  { ... }
  component "<type>" "<name>" { ... }
  action "<type>" "<name>"    { ... }
  output "<name>"    { ... }
}
```

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

Variables without a default **must** be supplied via `--var` at runtime.

### `provider`

```hcl
provider "postgres" {
  dsn = var.dsn
}
```

The label selects which provider in `required_providers` is being configured.
The body is provider-specific — see the [providers reference](../../providers/).

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

### `action`

```hcl
action "sql" "create_merchant" {
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
| `retry` | block | Retry policy. See below. |
| `lifecycle` | block | Teardown configuration. See below. |

#### `retry` block

```hcl
action "request" "flaky" {
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
action "sql" "create_merchant" {
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

- `var.<name>` — scenario variables (from the record).
- `self.<field>` — this action's own recorded outputs. Use it to target the
  resources this action created, without restating the node name.
- `action.<type>.<name>.<field>` — any other action's recorded outputs, for
  teardowns that reference upstream state.

See [lifecycle](../../concepts/lifecycle/) for authoring guidance and the
[`self` namespace](../expressions/#self-teardown-only) under expressions.

### `output`

```hcl
output "merchant_id" {
  value = action.sql.create_merchant.id
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
  variable "<name>" { ... }
  action "<type>" "<name>" { ... }
  output "<name>"   { ... }
}
```

The label is the component's type. Component files may not declare
`required_providers` or `provider` blocks — components inherit providers from
the enclosing scenario.

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
5. **Topological sort**, then execute each node:
   - For an action: evaluate its body, call provider `Execute`, record outputs.
   - For a component: resolve inputs, execute internal actions, resolve outputs.
6. **Evaluate scenario outputs** with all node results in scope.

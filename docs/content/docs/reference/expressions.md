---
title: Expressions
weight: 30
---

# Expression reference

Orchard uses HCL expressions with a cty type system. This page covers the
reference namespaces, type expressions, and available functions.

## Reference namespaces

Five reference namespaces are available inside expressions:

| Form | Scope | Available when |
|------|-------|----------------|
| `var.<name>` | Scenario (or component) variables. | Immediately — resolved up front. |
| `action.<type>.<name>.<output>` | Action outputs. | After the referenced action completes. |
| `component.<type>.<name>.<output>` | Component instance outputs. | After the component resolves. |
| `var.<name>` (inside a component) | The component's own variables. | Immediately, scoped to the component. |
| `self.<output>` | The current action's own outputs. | Inside `lifecycle { teardown { } }` blocks only. |

### Variable references

```hcl
provider "postgres" {
  dsn = var.dsn
}

action "postgres_query" "insert" {
  query = "INSERT INTO t (name) VALUES ('${var.name}')"
}
```

Variable references are resolved at plan time. They cannot participate in the
dependency graph — if you need ordering, reference an action or component
output instead.

### Action references

```hcl
action "postgres_query" "next" {
  query = "SELECT * FROM users WHERE id = ${action.postgres_query.previous.user_id}"
}

output "merchant_id" {
  value = action.postgres_query.create_merchant.id
}
```

`action.<type>.<name>.<output>` — all three segments are required. The
`<output>` segment depends on what the provider's action schema declares.

### Component references

```hcl
action "postgres_query" "attach" {
  query = "INSERT ... VALUES (${component.merchant.acme.id})"
}
```

`component.<type>.<name>.<output>` — where `<type>` matches the component's
declared name in its source file and `<name>` matches the instance label in
the scenario.

### `self` (teardown only) {#self-teardown-only}

Inside a `lifecycle { teardown { } }` block, `self.<field>` refers to the
action's own recorded outputs. This lets authors write teardowns without
repeating the node name:

```hcl
action "postgres_query" "create_merchant" {
  query = "INSERT INTO merchants (name) VALUES ('${var.name}') RETURNING id"

  lifecycle {
    teardown {
      query = "DELETE FROM merchants WHERE id = ${self.id}"
    }
  }
}
```

`self` is only populated when `orchard teardown <record>` evaluates the body;
the values come from the record's per-node outputs captured at run time. Inside
the action's main body (during `orchard run`), `self` is not in scope.

## Type expressions

Types are written as cty type expressions:

| Expression | Meaning |
|------------|---------|
| `string` | String. |
| `number` | Number (integer or float). |
| `bool` | Boolean. |
| `list(T)` | List of T. |
| `set(T)` | Set of T. |
| `map(T)` | Map from string to T. |
| `object({ k = T, ... })` | Object with fixed keys. |
| `tuple([T1, T2, ...])` | Tuple with positional types. |
| `any` | Any cty value (dynamic type). |

Examples:

```hcl
variable "count" {
  type    = number
  default = 1
}

variable "tags" {
  type    = list(string)
  default = ["demo", "dev"]
}

variable "config" {
  type = object({
    retries = number
    timeout = string
  })
}
```

Omitting `type` is equivalent to declaring `type = any`.

## String interpolation

HCL supports `${ ... }` interpolation inside strings:

```hcl
query = "INSERT INTO merchants (name) VALUES ('${var.name}')"
```

Quote carefully when interpolating into SQL, shell commands, or HTTP URLs —
Orchard does not escape values for you.

## Operators

Standard HCL operators are available:

- Arithmetic: `+`, `-`, `*`, `/`, `%`
- Comparison: `==`, `!=`, `<`, `<=`, `>`, `>=`
- Logical: `&&`, `||`, `!`
- Conditional: `cond ? a : b`

```hcl
variable "environment" { default = "dev" }

action "postgres_query" "seed" {
  query = var.environment == "prod" ? "SELECT 1" : "INSERT INTO ..."
}
```

## Functions

See the [functions reference](../functions/) for the complete list of all
available functions with signatures, descriptions, parameter tables, and
examples. The reference is auto-generated from Orchard's function registry
so it stays in sync with the binary.

## Reference syntax in `depends_on`

`depends_on` accepts references as bare traversals — not strings:

```hcl
action "postgres_query" "b" {
  depends_on = [action.postgres_query.a, component.merchant.acme]
  query      = "..."
}
```

These references don't pull any data into scope; they only create graph edges.

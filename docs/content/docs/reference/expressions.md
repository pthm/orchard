---
title: Expressions
weight: 30
---

# Expression reference

Orchard uses HCL expressions with a cty type system. This page covers the
reference namespaces, type expressions, and available functions.

## Reference namespaces

Four reference namespaces are available inside expressions:

| Form | Scope | Available when |
|------|-------|----------------|
| `var.<name>` | Scenario (or component) variables. | Immediately — resolved up front. |
| `action.<type>.<name>.<output>` | Action outputs. | After the referenced action completes. |
| `component.<type>.<name>.<output>` | Component instance outputs. | After the component resolves. |
| `var.<name>` (inside a component) | The component's own variables. | Immediately, scoped to the component. |

### Variable references

```hcl
provider "postgres" {
  dsn = var.dsn
}

action "sql" "insert" {
  query = "INSERT INTO t (name) VALUES ('${var.name}')"
}
```

Variable references are resolved at plan time. They cannot participate in the
dependency graph — if you need ordering, reference an action or component
output instead.

### Action references

```hcl
action "sql" "next" {
  query = "SELECT * FROM users WHERE id = ${action.sql.previous.user_id}"
}

output "merchant_id" {
  value = action.sql.create_merchant.id
}
```

`action.<type>.<name>.<output>` — all three segments are required. The
`<output>` segment depends on what the provider's action schema declares.

### Component references

```hcl
action "sql" "attach" {
  query = "INSERT ... VALUES (${component.merchant.acme.id})"
}
```

`component.<type>.<name>.<output>` — where `<type>` matches the component's
declared name in its source file and `<name>` matches the instance label in
the scenario.

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

action "sql" "seed" {
  query = var.environment == "prod" ? "SELECT 1" : "INSERT INTO ..."
}
```

## Built-in functions

HCL ships a standard library of functions (string manipulation, collections,
encoding, math). Common ones:

| Function | Example |
|----------|---------|
| `length` | `length(var.tags)` |
| `concat` | `concat(var.a, var.b)` |
| `format` | `format("hello %s", var.name)` |
| `upper` / `lower` | `upper(var.region)` |
| `jsonencode` | `body = jsonencode({ name = var.name })` |
| `jsondecode` | `jsondecode(action.request.x.body)` |
| `lookup` | `lookup(var.config, "timeout", "30s")` |

See the [HCL function reference](https://github.com/hashicorp/hcl/tree/main/ext/tryfunc)
for the full catalog.

## Reference syntax in `depends_on`

`depends_on` accepts references as bare traversals — not strings:

```hcl
action "sql" "b" {
  depends_on = [action.sql.a, component.merchant.acme]
  query      = "..."
}
```

These references don't pull any data into scope; they only create graph edges.

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

Orchard exposes a curated subset of the cty standard library in every
expression — variable defaults, provider configs, component inputs, action
attributes, scenario outputs, component outputs.

| Category | Functions |
|----------|-----------|
| Strings | `upper`, `lower`, `title`, `trim`, `trimspace`, `trimprefix`, `trimsuffix`, `substr`, `replace`, `format`, `formatlist`, `split`, `join`, `chomp`, `strlen` |
| Collections | `concat`, `length`, `keys`, `values`, `lookup`, `contains`, `merge`, `reverse`, `sort`, `slice`, `distinct`, `element`, `range`, `zipmap` |
| Encoding | `jsonencode`, `jsondecode`, `csvdecode` |
| Math | `abs`, `ceil`, `floor`, `max`, `min`, `log`, `pow`, `signum` |
| Regex | `regex`, `regexall` |
| Time | `formatdate`, `timeadd` |
| Sets | `setintersection`, `setproduct`, `setsubtract`, `setunion` |

Examples:

```hcl
variable "name" { default = upper("acme") }

provider "http" {
  base_url = format("https://%s.example.com", var.region)
}

action "request" "create" {
  method = "POST"
  body   = jsonencode({ name = var.name, tags = concat(var.base_tags, ["orchard"]) })
}
```

Note that string-length uses `strlen` (not `length`, which operates on
collections). Functions that take a collection — `length`, `concat`,
`sort` — require lists, maps, or tuples.

The function set is fixed at compile time. Adding new functions is additive
to existing scenarios and can be proposed via the repo.

## Reference syntax in `depends_on`

`depends_on` accepts references as bare traversals — not strings:

```hcl
action "sql" "b" {
  depends_on = [action.sql.a, component.merchant.acme]
  query      = "..."
}
```

These references don't pull any data into scope; they only create graph edges.

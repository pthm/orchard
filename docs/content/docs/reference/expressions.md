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

## Built-in functions

Orchard exposes a function library in every expression — variable defaults,
provider configs, component inputs, action attributes, scenario outputs,
component outputs, and teardown bodies. All names use lowercase compound
words with no separators (`jsonencode`, `timeadd`, `timeseries`, ...).

| Category | Functions |
|----------|-----------|
| Strings | `upper`, `lower`, `title`, `trim`, `trimspace`, `trimprefix`, `trimsuffix`, `substr`, `replace`, `format`, `formatlist`, `split`, `join`, `chomp`, `strlen` |
| Collections | `concat`, `length`, `keys`, `values`, `lookup`, `contains`, `merge`, `reverse`, `sort`, `slice`, `distinct`, `element`, `range`, `zipmap` |
| Encoding | `jsonencode`, `jsondecode`, `csvdecode` |
| Math | `abs`, `ceil`, `floor`, `max`, `min`, `log`, `pow`, `signum` |
| Regex | `regex`, `regexall` |
| Time | `now`, `timeadd`, `timesub`, `formatdate`, `timeunix`, `timerandom`, `timeseries`, `timesequence` |
| Identity | `uuid` |
| Environment | `env` |
| Sets | `setintersection`, `setproduct`, `setsubtract`, `setunion` |

### Time functions

| Function | Signature | Returns |
|----------|-----------|---------|
| `now` | `now()` | Current UTC as RFC3339 string |
| `timeadd` | `timeadd(timestamp, duration)` | Timestamp plus a Go duration |
| `timesub` | `timesub(timestamp, duration)` | Timestamp minus a duration (supports `d`, `w`) |
| `formatdate` | `formatdate(format, timestamp)` | Formatted timestamp string |
| `timeunix` | `timeunix(timestamp)` | Unix epoch seconds as number |
| `timerandom` | `timerandom(start, end)` | Single random timestamp in range |
| `timeseries` | `timeseries(start, end, count)` | Exactly N evenly-spaced timestamps |
| `timesequence` | `timesequence(start, end, min [, max])` | Timestamps stepping by random intervals |

Duration strings accept Go's standard units (`h`, `m`, `s`, `ms`) plus
`d` (days = 24h) and `w` (weeks = 7d). Fractional values like `"0.5d"` work.

### Examples

```hcl
variable "name"  { default = upper("acme") }
variable "id"    { default = uuid() }
variable "dates" { default = timeseries(timesub(now(), "90d"), now(), 30) }

provider "postgres" {
  dsn = env("DATABASE_URL")
}

action "http_request" "create" {
  method = "POST"
  body   = jsonencode({ name = var.name, id = var.id })
}

action "postgres_query" "insert" {
  query = format("INSERT INTO events (id, ts) VALUES ('%s', '%s')",
    uuid(), element(var.dates, 0))
}
```

Note: `strlen` measures string length; `length` measures collection length
(lists, maps, tuples). `now()`, `uuid()`, `timerandom()`, and `env()` are
impure — they may return different values on each call. That's intentional:
scenarios are single-shot imperative runs, not convergent plans.

The function set is fixed at compile time. Adding new functions is additive
to existing scenarios.

## Reference syntax in `depends_on`

`depends_on` accepts references as bare traversals — not strings:

```hcl
action "postgres_query" "b" {
  depends_on = [action.postgres_query.a, component.merchant.acme]
  query      = "..."
}
```

These references don't pull any data into scope; they only create graph edges.

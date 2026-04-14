---
title: Schema and types
weight: 30
---

# Schema and types

A provider's schema is its contract with Orchard. It describes what configuration
the provider accepts, what action types it handles, and what each action's
attributes and outputs look like. Orchard uses the schema to type-check scenarios
at plan time before a single action runs.

## `ProviderSchema`

```json
{
  "name": "stripe",
  "version": "0.3.0",
  "protocol": "1",
  "config": {
    "api_key": { "type": "string", "required": true, "description": "..." }
  },
  "actions": {
    "charge": { ... },
    "refund": { ... }
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Unique provider identifier. Users see this in error messages. |
| `version` | `string` | Provider version. Freeform string; use semver. |
| `protocol` | `string` | Wire protocol version. **Must be `"1"`** today. |
| `config` | `map[string]AttrSchema` | Attributes accepted in the `provider "name" { }` block. |
| `actions` | `map[string]ActionSchema` | Action types this provider handles, keyed by action type name. |

## `ActionSchema`

```json
{
  "description": "Execute a SQL statement and capture the first row",
  "attrs": {
    "query": { "type": "string", "required": true }
  },
  "outputs": { "type": "any" }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `description` | `string` | Free-text description. Surfaced in docs and errors. |
| `attrs` | `map[string]AttrSchema` | Attributes the action accepts. |
| `outputs` | `AttrSchema` (optional) | Shape of what the action returns. Omit if the action has no outputs. |

## `AttrSchema`

```json
{
  "type": "string",
  "required": true,
  "default": "\"GET\"",
  "description": "HTTP method"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | `string` | Textual cty type expression. See below. |
| `required` | `bool` | Whether the attribute must be provided. |
| `default` | JSON raw | Optional default value, JSON-encoded cty. Applied when the attribute is omitted. |
| `description` | `string` | Free-text description. |

## Type expressions

Types are written as cty type expressions in string form:

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

```
string
number
list(string)
map(number)
object({ id = number, name = string })
object({ status = number, body = string, headers = map(string) })
any
```

Omitting `type` entirely is equivalent to `any`.

## Authoring the schema in HCL

Orchard provides a schema authoring format in HCL so you don't hand-write JSON.
Save it as `schema.hcl`, parse it with `protocol.ParseHCLSchema`, and emit it as
JSON from your `schema` subcommand.

```hcl
provider "stripe" {
  version  = "0.3.0"
  protocol = "1"
  description = "Stripe payments provider"

  config {
    attribute "api_key" {
      type        = string
      required    = true
      description = "Stripe secret key"
    }
    attribute "api_version" {
      type        = string
      default     = "2023-10-16"
      description = "Stripe API version header"
    }
  }

  action "charge" {
    description = "Create a charge against a customer"

    attribute "amount" {
      type     = number
      required = true
    }
    attribute "currency" {
      type     = string
      required = true
    }
    attribute "customer" {
      type     = string
      required = true
    }

    output {
      type = object({
        id     = string,
        status = string,
        amount = number,
      })
    }
  }
}
```

The HCL form is equivalent to the JSON form and round-trips through the same
`ProviderSchema` struct.

## Cty values on the wire

Attributes and outputs are sent as JSON-encoded cty values, not raw JSON.
The difference matters for types that don't have a direct JSON analog (numbers
preserve full precision, null is distinct from missing, object attributes are
declared rather than inferred).

For most providers the distinction is invisible — strings encode as JSON
strings, numbers as JSON numbers, booleans as JSON booleans. But when you need
a canonical representation — especially for numbers or when round-tripping
values — use `pkg/protocol/ctyjson.go` in Go, or mirror its behavior in other
languages.

### Wire example

Given this action body:

```hcl
action "charge" "test" {
  amount   = 1000
  currency = "usd"
  customer = "cus_123"
}
```

Orchard sends:

```json
{
  "id": 3,
  "method": "execute",
  "params": {
    "action": "charge",
    "attrs": {
      "amount":   "1000",
      "currency": "\"usd\"",
      "customer": "\"cus_123\""
    }
  }
}
```

Each value in `attrs` is a separately JSON-encoded cty value. The provider
decodes them back into typed values using the declared schema types.

## Defaults and coercion

When a scenario omits an attribute that has a `default`, Orchard fills in the
default before calling `execute`. The provider sees the value every time; it
doesn't need default-handling logic.

Coercion also happens before dispatch. If the schema declares `type = number`
and the scenario passes a string-typed variable, Orchard attempts coercion and
fails at plan time if the value can't be converted. The provider receives
well-typed values.

## Outputs

The `outputs` field of an action schema declares the shape of the object the
action returns. If present, Orchard uses it to type-check downstream references
like `action.charge.test_charge.id`.

Use `any` for outputs whose shape depends on runtime (for example,
`builtin/postgres`'s `sql` action — its outputs depend on the columns returned
by each specific query). Use a concrete `object({ ... })` when the shape is
fixed; it makes scenario authors' lives easier.

## Validation

`ProviderSchema.Validate()` in `pkg/protocol/schema.go` checks the schema for
self-consistency. Non-Go providers should implement equivalent checks before
serving their schema, or Orchard will reject them at handshake time:

- `name`, `version`, and `protocol` are non-empty.
- `protocol` matches the Orchard `ProtocolVersion`.
- At least one action is declared.
- Every `type` expression in config, action attrs, and outputs parses as a valid
  cty type.

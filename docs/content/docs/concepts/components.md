---
title: Components
weight: 20
---

# Components

A **component** is a reusable unit of state. It's the Orchard equivalent of a
function: it takes inputs, performs some work, and returns outputs. Scenarios
compose components to describe complex state without repeating themselves.

## Defining a component

A component lives in its own file. The file contains one `component "<name>" { ... }`
block:

```hcl
# components/merchant.hcl
component "merchant" {
  variable "name" {
    default = "Default Merchant"
  }

  variable "region" {
    default = "us"
  }

  action "postgres_query" "create" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.name}', '${var.region}') RETURNING id, name, region"
  }

  output "id"     { value = action.postgres_query.create.id }
  output "name"   { value = action.postgres_query.create.name }
  output "region" { value = action.postgres_query.create.region }
}
```

A component has the same vocabulary as a scenario — variables, actions, outputs —
but no `required_providers` or `provider` blocks. Components inherit their providers
from the enclosing scenario.

## Using a component

Scenarios instantiate a component with `component "<type>" "<name>" { ... }`:

```hcl
component "merchant" "acme" {
  source = "../components/merchant.hcl"
  inputs = {
    name   = "Acme Corp"
    region = "us"
  }
}
```

- **Two labels.** The first matches the component's declared name (`merchant`).
  The second is an instance name (`acme`) — unique within the scenario.
- **`source`** is a path to the component file, resolved relative to the file
  that contains the `component` block.
- **`inputs`** is an object literal. Keys must match the component's declared
  variables; required variables (those with no default) must be present.

## Consuming component outputs

Component instances expose their outputs as `component.<type>.<name>.<output>`:

```hcl
action "postgres_query" "create_product" {
  query = "INSERT INTO products (merchant_id, name, price) VALUES (${component.merchant.acme.id}, 'Widget', 9.99) RETURNING id"
}
```

Orchard parses the reference, adds a dependency edge, and runs the `merchant.acme`
component before `create_product`.

## Why use components

- **Reuse.** A well-designed `merchant` component can be instantiated many times
  across many scenarios with different inputs.
- **Abstraction.** A component hides the SQL/HTTP/exec details behind a named
  interface. Scenario authors think in terms of *"a merchant"*, not in terms of
  three SQL inserts.
- **Testability.** Components can be exercised in isolation with a scenario that
  instantiates just that component.

## When *not* to use components

- **One-off work.** If the SQL or HTTP call only appears in one scenario, just use
  an `action` directly. Don't abstract prematurely.
- **State that spans scenarios.** Components are stateless definitions — they
  describe how to produce state, not how to track it across runs. If you need
  state persistence, that belongs in the target system, not in Orchard.

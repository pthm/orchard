---
title: Composing components
weight: 50
---

**Goal.** Break a large scenario down into reusable components. Think of
components like functions: small, well-named, tested in isolation, reused across
scenarios.

## When to extract a component

Extract a component when you notice one of these signals:

- **Repetition.** The same 5–10 lines of HCL appear in three scenarios. Make
  them a component.
- **Named concepts.** When you find yourself wanting to say *"a merchant"*
  instead of *"three SQL inserts and two HTTP calls"*, you've found the
  component boundary.
- **Scope variation.** A small concept today might need to expand. A component
  gives you a single place to change behavior for all callers.

Don't extract:

- **One-off work.** If the block only appears once, leave it inline.
- **Before you know the shape.** Premature components are harder to fix than
  duplicated HCL — write it out twice, then extract on the third.

## Anatomy of a good component

A component has a focused purpose, a short input surface, and a minimal output
surface.

```hcl
# components/merchant.hcl
component "merchant" {
  variable "name" {
    type     = string
  }

  variable "region" {
    type    = string
    default = "us"
  }

  variable "verified" {
    type    = bool
    default = false
  }

  action "postgres_query" "insert" {
    query = "INSERT INTO merchants (name, region, verified) VALUES ('${var.name}', '${var.region}', ${var.verified}) RETURNING id, name, region, verified"
  }

  output "id"       { value = action.postgres_query.insert.id }
  output "name"     { value = action.postgres_query.insert.name }
  output "region"   { value = action.postgres_query.insert.region }
  output "verified" { value = action.postgres_query.insert.verified }
}
```

Notes:

- **Typed variables** with defaults where sensible. Required variables — here,
  `name` — have no default.
- **Outputs expose useful IDs and derived fields.** The caller shouldn't have to
  know how `id` is generated.
- **One job.** This component creates a merchant. It doesn't create products,
  users, or payments — those are separate components that compose with this one.

## Composing components that depend on each other

```hcl
scenario "merchant_with_products" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "acme" {
    source = "../components/merchant.hcl"
    inputs = { name = "Acme Corp" }
  }

  component "product" "widget" {
    source = "../components/product.hcl"
    inputs = {
      merchant_id = component.merchant.acme.id
      name        = "Widget"
      price       = 9.99
    }
  }

  component "product" "gadget" {
    source = "../components/product.hcl"
    inputs = {
      merchant_id = component.merchant.acme.id
      name        = "Gadget"
      price       = 19.99
    }
  }
}
```

The `product` component is instantiated twice with different names. Orchard
wires each instance's `merchant_id` input from the same merchant's output,
creating a diamond in the dependency graph — both products depend on the
merchant but not on each other.

## Pattern: components that call other components

Components can't instantiate other components directly today — only scenarios
can. If you want a "merchant with products" macro, the composition lives at the
scenario level, or in a meta-scenario that's called from other scenarios.

## Naming conventions

- **Component file name = component name.** `components/merchant.hcl` contains
  `component "merchant" { }`.
- **Instance name describes the role.** `component "merchant" "acme"` is clearer
  than `component "merchant" "one"` when the scenario has multiple instances.
- **Lowercase, snake_case.** `monthly_revenue`, `created_at`, `api_token` —
  match the column names of the systems you're driving.

## Keeping components healthy

- **Keep the input surface narrow.** If a component has 15 inputs, it's
  probably doing too much. Split it.
- **Prefer outputs over implicit state.** If callers care about a field,
  expose it. Don't make them query the database to find what you just
  inserted.
- **Test components in isolation.** A small scenario that instantiates just
  one component makes debugging much easier than chasing through a five-level
  composition.

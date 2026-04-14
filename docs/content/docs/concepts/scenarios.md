---
title: Scenarios
weight: 10
---

# Scenarios

A **scenario** is the top-level entrypoint in Orchard. It is the thing a user runs. A
scenario composes components, accepts parameters via variables, and produces outputs.

```hcl
scenario "merchant_with_chargeback" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "merchant_name" {
    default = "Acme Corp"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "base" {
    source = "../components/merchant.hcl"
    inputs = { name = var.merchant_name }
  }
}
```

A scenario should read like a statement of intent: *"a merchant with a chargeback"*,
not a sequence of SQL statements. The scenario author describes what they want;
Orchard and the components figure out how to produce it.

---
title: Getting started
weight: 10
---

# Getting started

This page walks through installing Orchard, pointing it at a Postgres database, and
running your first scenario.

## Install

Orchard ships as a single Go binary.

```sh
go install github.com/pthm/orchard@latest
```

Verify the install:

```sh
orchard version
```

## Your first scenario

Create `scenarios/hello.hcl`:

```hcl
scenario "hello" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {
    default = "postgres://localhost:5432/orchard?sslmode=disable"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "acme" {
    source = "./components/merchant.hcl"
    inputs = { name = "Acme Corp" }
  }
}
```

Run it:

```sh
orchard apply scenarios/hello.hcl
```

Orchard builds the execution plan, applies each component in dependency order, and
reports the resulting state. Tear it down with `orchard destroy`.

## Next steps

- Read [Concepts](../concepts/) to understand how scenarios, components, and providers
  relate.
- Browse the [Reference](../reference/) for the full HCL schema.

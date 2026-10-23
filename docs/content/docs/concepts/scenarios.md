---
title: Scenarios
weight: 10
---

A **scenario** is the top-level entrypoint in Orchard. It is the thing a user runs.
A scenario declares the providers it needs, accepts parameters via variables,
composes components and actions, and exposes outputs.

```hcl
scenario "merchant_with_chargeback" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}
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

  output "merchant_id" {
    value = component.merchant.base.id
  }
}
```

## What goes inside a scenario

A scenario file contains exactly one `scenario "<name>" { ... }` block. Inside that
block, you can declare:

| Block | Purpose |
|-------|---------|
| `required_providers` | Which providers the scenario uses and where they come from. |
| `variable` | Named inputs to the scenario, with optional type and default. |
| `provider` | Configuration for a provider declared in `required_providers`. |
| `component` | An instance of a reusable component, with `source` and `inputs`. |
| `action` | A single unit of work executed by a provider. |
| `output` | Values exposed to the caller when the scenario finishes. |

Block ordering doesn't matter — Orchard resolves dependencies from the expressions
in each block, not from source order.

## Statement of intent

A scenario should read like a statement of intent: *"a merchant with a chargeback"*,
not a sequence of SQL statements. The scenario author describes what they want;
components and providers figure out how to produce it.

In practice that means:

- Use **variables** for the dimensions you want to vary across runs (merchant name,
  region, DSN). Don't hard-code.
- Use **components** to package up recurring state — a merchant with its settings, an
  order with its line items — so scenarios stay short and composable.
- Use **actions** directly when the work is one-off or scenario-specific.
- Use **outputs** to surface the bits of state a caller will need to interact with
  the resulting environment (IDs, URLs, tokens).

## Metadata

Scenarios can carry a `description` and `tags` for discoverability:

```hcl
scenario "merchant_with_chargeback" {
  description = "Merchant with three failed payments and a pending review"
  tags        = ["demo", "chargebacks"]
  # ...
}
```

`orchard list` surfaces both — the description appears under the scenario name,
and tags render in brackets alongside it.

## Multiple scenarios per file

A single `.hcl` file can declare more than one scenario block. This is useful
for grouping related variants (a demo vs a load-test version of the same setup)
without splitting into many files.

When a file has multiple scenarios, `orchard run` and `orchard plan` require
`--scenario <name>` to pick one. Single-scenario files still work with no flag.

```sh
orchard run  scenarios/uat.hcl --scenario happy_path
orchard plan scenarios/uat.hcl --scenario edge_case
```

## Running a scenario

```sh
orchard init my-project                 # scaffold a starter project
orchard plan scenarios/merchant.hcl     # validate, print the graph, no side effects
orchard run  scenarios/merchant.hcl     # execute it for real
orchard list scenarios/                 # list all scenarios in a directory
```

See the [CLI reference](/reference/cli/) for flags and exit codes.

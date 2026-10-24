---
title: Seeding a demo environment
weight: 10
---

**Goal.** Produce a database that tells a compelling story when someone opens the
product. Not just rows — a believable narrative: customers with histories, orders
with realistic timing, payments with a mix of outcomes.

**Why Orchard.** Hand-written SQL seed scripts rot. They drift from the schema,
they can't vary the story for different audiences, and they don't compose. A
scenario describes the story once; variables let you reshape it per demo.

## Pattern: scenario per persona

Define one scenario per demo persona. Each scenario composes the same components
with different inputs.

```
scenarios/
├── demo_enterprise.hcl    # one merchant, high volume, low chargeback rate
├── demo_smb.hcl           # dozens of merchants, spiky data, typical chargebacks
└── demo_troubled.hcl      # one merchant, flagged transactions, support tickets
```

## Example: enterprise demo

```hcl
scenario "demo_enterprise" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "acme" {
    source = "../components/merchant.hcl"
    inputs = {
      name           = "Acme Corporation"
      region         = "us"
      monthly_volume = 2500000
      verified       = true
    }
  }

  component "product_catalog" "acme_catalog" {
    source = "../components/product_catalog.hcl"
    inputs = {
      merchant_id = component.merchant.acme.id
      sku_count   = 120
    }
  }

  component "order_history" "last_quarter" {
    source = "../components/order_history.hcl"
    inputs = {
      merchant_id = component.merchant.acme.id
      days        = 90
      order_rate  = 45     # orders/day
      avg_amount  = 280.00
    }
  }

  output "merchant_id"   { value = component.merchant.acme.id }
  output "merchant_name" { value = component.merchant.acme.name }
}
```

The scenario reads like a description: *"Acme Corp with a 120-SKU catalog and 90
days of order history."* The components underneath generate rows.

## Generating believable data

Fake data and random functions fill in the detail, and a pinned `seed`
makes every run of the demo produce the same customers, so the story
doesn't change between rehearsal and the real thing:

```hcl
scenario "demo_smb" {
  seed = 2026

  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  action "postgres_insert" "customer" {
    count  = 50
    table  = "customers"
    values = {
      id      = uuidv7(timerandom(timesub(now(), "180d"), now()))
      name    = fake("name")
      email   = fake("email")
      company = fake("company")
      plan    = randweighted({ free = 70, pro = 25, enterprise = 5 })
      seats   = randint(1, 40)
      ref     = faketemplate("CUS-####-??")
    }
  }
}
```

`fake()` covers 364 kinds of data, from `name` and `address` to
`creditcard` and `productname`; see the
[functions reference](../../../reference/functions/fake-kinds/) for the
list. Drop the `seed` line, or pass `--seed`, for a different set of
customers; every run prints its seed, and `orchard run --seed <n>` repeats
it.

## Running a demo setup

```sh
orchard run scenarios/demo_enterprise.hcl \
  --var dsn="postgres://localhost/demo_db"
```

Before a demo, blow the database away and re-run. Orchard's output printing gives
you a URL or ID to log in with.

## Tips

- **Parameterize everything** that might vary per audience: names, regions, dates,
  volume. Don't hard-code "Acme Corp" inside a component.
- **Keep timing realistic.** If your components generate timestamps, let
  scenarios control the time window — a "six months ago" demo looks different
  from a "yesterday" demo.
- **Avoid live external APIs.** The HTTP provider is convenient for seeding
  integrations, but every external call makes your demo flakier. Mock or stub
  where you can.
- **Commit demo scenarios to your repo.** They document what a representative
  customer looks like — useful far beyond sales.

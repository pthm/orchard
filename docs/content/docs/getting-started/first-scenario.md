---
title: Your first scenario
linkTitle: First scenario
weight: 30
---

This page walks through writing and running a small scenario that inserts a merchant
row and a product row into Postgres, wiring the merchant's generated ID into the
product insert.

## Prerequisites

You'll need a PostgreSQL database you can write to and two tables:

```sql
CREATE TABLE merchants (
  id     SERIAL PRIMARY KEY,
  name   TEXT NOT NULL,
  region TEXT NOT NULL
);

CREATE TABLE products (
  id          SERIAL PRIMARY KEY,
  merchant_id INT NOT NULL REFERENCES merchants(id),
  name        TEXT NOT NULL,
  price       NUMERIC NOT NULL
);
```

## Write the scenario

Create `scenarios/hello.hcl`:

```hcl
scenario "hello" {
  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "dsn" {
    default = "postgres://localhost:5432/orchard_dev?sslmode=disable"
  }

  variable "merchant_name" {
    default = "Acme Corp"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  action "postgres_query" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.merchant_name}', 'us') RETURNING id, name"
  }

  action "postgres_query" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.postgres_query.create_merchant.id}, 'Widget', 9.99) RETURNING id, name"
  }

  output "merchant_id" {
    value = action.postgres_query.create_merchant.id
  }

  output "product_id" {
    value = action.postgres_query.create_product.id
  }
}
```

A few things to notice:

- **`required_providers`** declares which providers the scenario uses and where
  they come from. `builtin/postgres` is compiled into the `orchard` binary.
- **`action "postgres_query" "create_merchant"`** is an action of type `postgres_query`
  (supplied by the postgres provider) named `create_merchant`. The `query`
  attribute is passed to the provider; the first returned row becomes the
  action's outputs.
- **`action.postgres_query.create_merchant.id`** is how the second action references the
  first action's output. Orchard parses this reference, adds an edge to the
  dependency graph, and runs `create_merchant` before `create_product`.
- **`output` blocks** expose values from the scenario when it finishes.

## Preview with `plan`

Before running, check the execution plan:

```sh
orchard plan scenarios/hello.hcl
```

`plan` validates the scenario, type-checks all references, and prints the graph.
**It does not open a database connection or run any actions.**

## Run it

```sh
orchard run scenarios/hello.hcl
```

Orchard configures the postgres provider, runs the two actions in dependency order,
and prints the declared outputs.

To override a variable:

```sh
orchard run scenarios/hello.hcl --var merchant_name="Globex"
```

## Next steps

- Read [Concepts](../../concepts/) to understand scenarios, components, providers,
  and wiring.
- Browse the [provider reference](/providers/) to see what each built-in
  provider can do.
- Check out [guides](../../guides/) for worked examples: demo data, local dev,
  integration testing.

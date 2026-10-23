---
title: Setup
weight: 20
---

Orchard is configuration-free: there is no global config file, no workspace init, and
no state directory. A project is just a directory of `.hcl` files.

## Recommended layout

```
my-project/
├── scenarios/
│   ├── dev.hcl
│   ├── demo.hcl
│   └── integration.hcl
└── components/
    ├── merchant.hcl
    └── order.hcl
```

- **`scenarios/`** — one file per runnable scenario. Scenario files are what you
  pass to `orchard run`.
- **`components/`** — reusable building blocks. Components are referenced from
  scenarios with `source = "../components/<name>.hcl"`.

This layout is a convention, not a requirement. Orchard doesn't care where files
live; `source` paths are resolved relative to the file that references them.

## PostgreSQL prerequisites

If you plan to use the `builtin/postgres` provider, you need:

- A reachable PostgreSQL server.
- A database the DSN can connect to.
- Any tables your scenarios insert into — Orchard does **not** create schema for
  you. Keep your schema migrations in the same repo and run them before your
  scenarios.

A typical local setup:

```sh
createdb orchard_dev
psql orchard_dev -f migrations/001_init.sql
```

## Variables and secrets

Scenarios declare their inputs with `variable` blocks. Pass values from the CLI:

```sh
orchard run scenarios/dev.hcl \
  --var dsn="postgres://localhost:5432/orchard_dev?sslmode=disable" \
  --var merchant_name="Acme Corp"
```

The `--var` flag can be repeated. Secrets belong in environment variables or a
secrets manager — feed them into `--var` from the shell. Orchard does not read
`.env` files or a dedicated variables file.

## What's next

You're set up. Write your [first scenario](../first-scenario/).

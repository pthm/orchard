---
title: Providers
weight: 30
---

A **provider** is the runtime that turns a declarative HCL action into a real side
effect. `builtin/postgres`, `builtin/mysql`, and `builtin/sqlite` run SQL. `builtin/redis` writes keys and hashes. `builtin/http` makes
HTTP requests. `builtin/exec` spawns child processes. Custom providers can do
anything you can write a binary for.

## The two kinds of providers

**Built-in providers** are compiled into the `orchard` binary. They're always
available and need no installation. Today that's:

- `builtin/postgres`
- `builtin/mysql`
- `builtin/sqlite`
- `builtin/redis`
- `builtin/http`
- `builtin/exec`

**External providers** are separate binaries that speak Orchard's
[provider protocol](../../extending/provider-protocol/) over stdio. They're
referenced by path:

```hcl
required_providers {
  stripe = { source = "exec:./bin/orchard-stripe" }
}
```

Built-in and external providers behave identically from a scenario author's point
of view — the same schema model, the same action mechanics, the same wiring. The
only difference is the source string.

## Declaring and configuring

Every provider a scenario uses must appear in `required_providers`:

```hcl
required_providers {
  postgres = { source = "builtin/postgres" }
  http     = { source = "builtin/http" }
}
```

If the provider takes configuration, declare it separately:

```hcl
provider "postgres" {
  dsn = var.dsn
}

provider "http" {
  base_url = "https://api.example.com"
  default_headers = {
    Authorization = "Bearer ${var.api_token}"
  }
}
```

Provider configuration is evaluated once per scenario run, before any actions
execute. It can reference variables but not action or component outputs — providers
must be configured before the actions that use them.

## Actions

Each provider declares one or more **action types** it handles. An action block
chooses the type with its first label:

```hcl
action "postgres_query"     "create_merchant" { query   = "..." }    # postgres
action "http_request" "fetch_user"      { url     = "..." }    # http
action "exec_run"     "backup_db"       { command = "..." }    # exec
```

Orchard looks up which provider offers the action type and routes the call there.
If multiple providers offer the same action type, disambiguate with an explicit
`provider` attribute:

```hcl
action "postgres_query" "write_analytics" {
  provider = "analytics_db"
  query    = "INSERT ..."
}
```

## Provider schemas

Every provider publishes a **schema** describing what it accepts. Orchard uses the
schema to type-check scenarios at plan time — catching typos, wrong types, and
missing required attributes before any action runs.

The schema covers:

- **Config attributes** — what goes in the `provider "name" { }` block.
- **Action types** — what action blocks the provider handles.
- **Attributes per action** — what fields each action accepts.
- **Outputs per action** — what values the action produces for downstream wiring.

See the [provider reference](/providers/) for the schema of each built-in
provider, and [writing a provider](../../extending/writing-a-provider/) to build
your own.

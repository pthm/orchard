---
title: Local development environments
weight: 30
---

**Goal.** New engineers clone the repo, run one command, and have a working local
database with enough data to develop against. No "ask Slack for a SQL dump."

**Why Orchard.** A single authoritative scenario replaces the tribal-knowledge
approach. The scenario lives in the repo and evolves with the schema.

## Pattern: one "dev" scenario per service

```
scenarios/
└── dev.hcl
```

`dev.hcl` is the canonical "everything you need to boot up locally." Engineers
run it after applying migrations.

## Example

```hcl
scenario "dev" {
  required_providers {
    postgres = { source = "builtin/postgres" }
    exec     = { source = "builtin/exec" }
  }

  variable "dsn" {
    default = "postgres://localhost:5432/myapp_dev?sslmode=disable"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  provider "exec" {}

  # Confirm the database is reachable before we try to seed it.
  action "exec_run" "check_db" {
    command = "psql"
    args    = [var.dsn, "-c", "SELECT 1"]
  }

  component "admin" "seed_admin" {
    source = "../components/user.hcl"
    inputs = {
      email = "admin@local.dev"
      role  = "admin"
    }
  }

  component "fixtures" "dev_data" {
    source = "../components/dev_fixtures.hcl"
    inputs = {
      admin_id = component.admin.seed_admin.id
    }
  }

  output "admin_email"   { value = component.admin.seed_admin.email }
  output "login_url"     { value = "http://localhost:3000/login" }
}
```

The engineer runs:

```sh
make db-setup              # runs migrations
orchard run scenarios/dev.hcl
```

And sees:

```
admin_email = admin@local.dev
login_url   = http://localhost:3000/login
```

## Pattern: object storage

A local S3 (SeaweedFS, LocalStack, or MinIO) starts empty, so the app's
buckets need creating before anything can upload. `s3_create_bucket` is a
no-op when the bucket is already there, so the dev scenario can always run it:

```hcl
provider "s3" {
  endpoint          = "http://localhost:8333"
  path_style        = true
  access_key_id     = "dev"
  secret_access_key = "dev"
}

action "s3_create_bucket" "uploads" {
  bucket = "app-uploads"
}

action "s3_put_object" "avatar" {
  bucket = action.s3_create_bucket.uploads.bucket
  key    = "avatars/${component.admin.seed_admin.id}.png"
  file   = "./fixtures/avatar.png"
}
```

## Pattern: idempotent vs destructive

Local dev scenarios are typically **destructive** — they assume a fresh
database. That's fine; document it. The alternative is writing idempotent SQL
(`INSERT ... ON CONFLICT DO UPDATE`), which works but complicates the scenario.

For most teams: "blow it away and re-seed" is the right default. Add a
`make db-reset` that drops and recreates the database, then runs the dev
scenario.

## Tips

- **Keep it fast.** Local dev is a daily ritual — the scenario should finish in
  seconds, not minutes. Pare down the data to what's needed for day-to-day
  work.
- **Split heavy data into opt-in scenarios.** Keep `dev.hcl` minimal and lean;
  offer `dev_heavy.hcl` for engineers working on reporting or perf.
- **Document required env vars in one place.** If the scenario uses `--var
  stripe_api_key=...`, say so in the repo's README — Orchard will tell you
  about the missing variable, but a pointer up front is kinder.

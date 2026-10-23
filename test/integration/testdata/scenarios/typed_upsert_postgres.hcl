scenario "typed_upsert_postgres" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  # Conflicts on the primary key (the default conflict columns).
  action "postgres_insert" "existing" {
    table       = "merchants"
    values      = { id = 100, name = "Legacy Ltd" }
    on_conflict = "update"

    lifecycle {
      managed_teardown = true
    }
  }

  action "postgres_insert" "new" {
    table       = "merchants"
    values      = { id = 101, name = "Fresh" }
    on_conflict = "update"
  }
}

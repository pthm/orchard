scenario "typed_restore_postgres" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  action "postgres_insert" "acme" {
    table  = "merchants"
    values = { name = "Acme" }
  }

  # Owned: restored on teardown by default.
  action "postgres_update" "rename_acme" {
    table = "merchants"
    set   = { name = "Acme Ltd" }
    where = { id = action.postgres_insert.acme.id }
  }

  # Pre-existing, so restored only because it's asked for.
  action "postgres_update" "rename_legacy" {
    table = "merchants"
    set   = { name = "Legacy Ltd" }
    where = { name = "Legacy" }

    lifecycle {
      managed_teardown = true
    }
  }
}

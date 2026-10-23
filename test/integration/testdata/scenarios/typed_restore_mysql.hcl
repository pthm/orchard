scenario "typed_restore_mysql" {
  required_providers {
    mysql = { source = "builtin/mysql" }
  }

  variable "dsn" {}

  provider "mysql" {
    dsn = var.dsn
  }

  action "mysql_insert" "acme" {
    table  = "merchants"
    values = { name = "Acme" }
  }

  # Owned: restored on teardown by default.
  action "mysql_update" "rename_acme" {
    table = "merchants"
    set   = { name = "Acme Ltd" }
    where = { id = action.mysql_insert.acme.id }
  }

  # Pre-existing, so restored only because it's asked for.
  action "mysql_update" "rename_legacy" {
    table = "merchants"
    set   = { name = "Legacy Ltd" }
    where = { name = "Legacy" }

    lifecycle {
      managed_teardown = true
    }
  }
}

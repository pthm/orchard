scenario "typed_upsert_mysql" {
  required_providers {
    mysql = { source = "builtin/mysql" }
  }

  variable "dsn" {}

  provider "mysql" {
    dsn = var.dsn
  }

  action "mysql_insert" "existing" {
    table            = "accounts"
    values           = { email = "legacy@example.com", name = "Legacy Ltd" }
    on_conflict      = "update"
    conflict_columns = ["email"]

    lifecycle {
      managed_teardown = true
    }
  }

  action "mysql_insert" "new" {
    table            = "accounts"
    values           = { email = "fresh@example.com", name = "Fresh" }
    on_conflict      = "ignore"
    conflict_columns = ["email"]
  }

  output "existing_id" { value = action.mysql_insert.existing.id }
}

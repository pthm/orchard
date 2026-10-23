scenario "typed_upsert" {
  required_providers {
    sqlite = { source = "builtin/sqlite" }
  }

  variable "path" {}

  provider "sqlite" {
    path = var.path
  }

  # Existing row, left alone: returned as it is.
  action "sqlite_insert" "existing_kept" {
    table            = "accounts"
    values           = { email = "kept@example.com", name = "Changed" }
    on_conflict      = "ignore"
    conflict_columns = ["email"]
  }

  # Existing row, updated. Not created by this run, so not restored
  # unless asked.
  action "sqlite_insert" "existing_updated" {
    table            = "accounts"
    values           = { email = "updated@example.com", name = "Updated" }
    on_conflict      = "update"
    conflict_columns = ["email"]
  }

  # Existing row, updated, and asked to restore it.
  action "sqlite_insert" "existing_restored" {
    table            = "accounts"
    values           = { email = "restored@example.com", name = "Updated" }
    on_conflict      = "update"
    conflict_columns = ["email"]

    lifecycle {
      managed_teardown = true
    }
  }

  # No existing row: an ordinary insert, deleted on teardown.
  action "sqlite_insert" "new" {
    table            = "accounts"
    values           = { email = "new@example.com", name = "New" }
    on_conflict      = "update"
    conflict_columns = ["email"]
  }

  output "kept_name" { value = action.sqlite_insert.existing_kept.name }
  output "kept_id"   { value = action.sqlite_insert.existing_kept.id }
  output "new_id"    { value = action.sqlite_insert.new.id }
}

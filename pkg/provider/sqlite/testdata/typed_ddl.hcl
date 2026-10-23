scenario "typed_ddl" {
  required_providers {
    sqlite = { source = "builtin/sqlite" }
  }

  variable "path" {}

  provider "sqlite" {
    path = var.path
  }

  # The table doesn't exist before the run, so the insert is checked
  # when it runs, after this creates it.
  action "sqlite_query" "schema" {
    query = "CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)"
  }

  action "sqlite_insert" "note" {
    depends_on = [action.sqlite_query.schema]
    table      = "notes"
    values     = { body = "hello" }
  }

  output "note_id" { value = action.sqlite_insert.note.id }
}

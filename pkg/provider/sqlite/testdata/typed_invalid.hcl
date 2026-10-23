scenario "typed_invalid" {
  required_providers {
    sqlite = { source = "builtin/sqlite" }
  }

  variable "path" {}

  provider "sqlite" {
    path = var.path
  }

  # Valid, and first in order: it must not run, because the next action
  # fails validation before the run starts.
  action "sqlite_query" "seed" {
    query = "INSERT INTO merchants (name, region) VALUES ('Seed', 'us')"
  }

  action "sqlite_insert" "broken" {
    depends_on = [action.sqlite_query.seed]
    table      = "merchants"
    values     = { nme = "Acme" }
  }
}

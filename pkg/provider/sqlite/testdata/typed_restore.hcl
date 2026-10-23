scenario "typed_restore" {
  required_providers {
    sqlite = { source = "builtin/sqlite" }
  }

  variable "path" {}

  provider "sqlite" {
    path = var.path
  }

  action "sqlite_insert" "acme" {
    table  = "merchants"
    values = { name = "Acme", region = "us" }
  }

  # Acme was inserted by this run, so its old region is restored on
  # teardown by default (then the insert's undo deletes it).
  action "sqlite_update" "move_acme" {
    table = "merchants"
    set   = { region = "eu" }
    where = { id = action.sqlite_insert.acme.id }
  }

  # Legacy existed before the run: not restored unless asked.
  action "sqlite_update" "move_legacy" {
    table = "merchants"
    set   = { region = "apac" }
    where = { name = "Legacy" }
  }

  # Asked for: restored even though Orchard didn't create the row.
  action "sqlite_update" "move_forced" {
    table = "merchants"
    set   = { region = "apac" }
    where = { name = "Forced" }

    lifecycle {
      managed_teardown = true
    }
  }

  # Opted out, though the row is owned.
  action "sqlite_update" "rename_acme" {
    depends_on = [action.sqlite_update.move_acme]
    table      = "merchants"
    set        = { name = "Acme Ltd" }
    where      = { id = action.sqlite_insert.acme.id }

    lifecycle {
      managed_teardown = false
    }
  }
}

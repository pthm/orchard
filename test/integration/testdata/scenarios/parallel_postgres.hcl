scenario "parallel_postgres" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  # Twenty independent inserts, run concurrently with --parallelism.
  action "postgres_insert" "m" {
    count  = 20
    table  = "merchants"
    values = { name = "m${count.index}" }
  }

  # Each renames its own row, so each is restored on teardown.
  action "postgres_update" "rename" {
    count = 20
    table = "merchants"
    set   = { name = "m${count.index} Ltd" }
    where = { id = action.postgres_insert.m[count.index].id }
  }
}

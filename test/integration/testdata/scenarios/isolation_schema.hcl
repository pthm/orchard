scenario "isolation_schema" {
  description = "Everything in a schema of its own; teardown drops the schema"
  tags        = ["postgres", "teardown", "isolation"]

  required_providers {
    pg = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  # One schema per run. The default is evaluated once and recorded, so
  # teardown drops the schema this run created.
  variable "schema" {
    default = "orchard_${substr(replace(uuid(), "-", ""), 0, 12)}"
  }

  # search_path sends unqualified table names to the run's schema. The
  # DSN already has a query string, hence `&`.
  provider "pg" {
    dsn = "${var.dsn}&search_path=${var.schema}"
  }

  action "postgres_query" "schema" {
    query = <<-SQL
      CREATE SCHEMA ${var.schema};
      CREATE TABLE ${var.schema}.merchants (
        id   BIGSERIAL PRIMARY KEY,
        name TEXT NOT NULL
      )
    SQL

    lifecycle {
      teardown {
        query = "DROP SCHEMA ${var.schema} CASCADE"
      }
    }
  }

  # Rows need no teardown of their own: dropping the schema removes them.
  action "postgres_insert" "acme" {
    depends_on = [action.postgres_query.schema]
    table      = "merchants"
    values     = { name = "Acme" }

    lifecycle {
      managed_teardown = false
    }
  }

  action "postgres_query" "bulk" {
    depends_on = [action.postgres_query.schema]
    query      = "INSERT INTO merchants (name) SELECT 'm' || n FROM generate_series(1, 50) n"
  }

  output "schema" { value = var.schema }
}

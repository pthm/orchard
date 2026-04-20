scenario "aliased_postgres" {
  description = "Postgres registered under a non-default alias to verify config routing"
  tags        = ["postgres", "alias", "routing"]

  # Register the postgres provider under a non-default local name.
  # Without the alias-based routing fix, Configure would look up
  # config under "postgres" (the schema name) and miss the "db"
  # block below — the scenario would fail with "dsn is required"
  # even though dsn is clearly provided.
  required_providers {
    db = {
      source = "builtin/postgres"
    }
  }

  variable "dsn" {
    default = ""
  }

  provider "db" {
    dsn = var.dsn
  }

  action "postgres_query" "ping" {
    query = "SELECT 1 AS ok"
  }

  output "ok" {
    value = action.postgres_query.ping.ok
  }
}

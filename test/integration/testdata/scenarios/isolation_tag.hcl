scenario "isolation_tag" {
  description = "Tag every row with the run; one teardown deletes by tag"
  tags        = ["postgres", "teardown", "isolation"]

  required_providers {
    pg = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  # Evaluated once and recorded, so teardown sees the run's tag.
  variable "run_tag" {
    default = substr(replace(uuid(), "-", ""), 0, 12)
  }

  provider "pg" {
    dsn = var.dsn
  }

  # Holds the cleanup for every row the run tags. It runs first, so
  # its teardown runs last, after anything that does its own.
  action "postgres_query" "sweep" {
    query = "SELECT '${var.run_tag}' AS run_tag"

    lifecycle {
      teardown {
        query = "DELETE FROM merchants WHERE name LIKE '%#${var.run_tag}'"
      }
    }
  }

  # A bulk insert with no per-row teardown; products follow their
  # merchant through ON DELETE CASCADE.
  action "postgres_query" "merchants" {
    depends_on = [action.postgres_query.sweep]
    query      = <<-SQL
      WITH m AS (
        INSERT INTO merchants (name)
        SELECT 'merchant ' || n || '#${var.run_tag}' FROM generate_series(1, 20) n
        RETURNING id
      )
      INSERT INTO products (merchant_id, name, price)
      SELECT id, 'widget', 9.99 FROM m
    SQL
  }

  output "run_tag" { value = var.run_tag }
}

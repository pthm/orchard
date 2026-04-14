scenario "provider_with_var" {
  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "dsn" {
    default = "postgres://localhost:5432/example"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  action "sql" "noop" {
    query = "SELECT 1"
  }
}

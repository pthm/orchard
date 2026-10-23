scenario "typed_postgres_invalid" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }

  # price is required, and "cheap" isn't a number.
  action "postgres_insert" "product" {
    table  = "products"
    values = { merchant_id = action.postgres_insert.merchant.id, name = "Widget", cost = "cheap" }
  }
}

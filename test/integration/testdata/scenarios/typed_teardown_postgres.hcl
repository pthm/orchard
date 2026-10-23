scenario "typed_teardown_postgres" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  # No lifecycle blocks: orchard teardown deletes both rows, product
  # first, from the undo steps recorded at run time.
  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }

  action "postgres_insert" "product" {
    table  = "products"
    values = { merchant_id = action.postgres_insert.merchant.id, name = "Widget", price = 9.99 }
  }

  output "merchant_id" { value = action.postgres_insert.merchant.id }
  output "product_id"  { value = action.postgres_insert.product.id }
}

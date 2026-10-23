scenario "typed_teardown_mysql" {
  required_providers {
    mysql = { source = "builtin/mysql" }
  }

  variable "dsn" {}

  provider "mysql" {
    dsn = var.dsn
  }

  # No lifecycle blocks: orchard teardown deletes both rows, product
  # first, from the undo steps recorded at run time.
  action "mysql_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }

  action "mysql_insert" "product" {
    table  = "products"
    values = { merchant_id = action.mysql_insert.merchant.id, name = "Widget", price = 9.99 }
  }

  output "merchant_id" { value = action.mysql_insert.merchant.id }
  output "product_id"  { value = action.mysql_insert.product.id }
}

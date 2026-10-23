scenario "typed" {
  required_providers {
    sqlite = { source = "builtin/sqlite" }
  }

  variable "path" {}

  provider "sqlite" {
    path = var.path
  }

  # An apostrophe would break a hand-built query; bind parameters don't care.
  action "sqlite_insert" "merchant" {
    table  = "merchants"
    values = { name = "O'Brien & Co", region = "us" }
  }

  action "sqlite_insert" "product" {
    table  = "products"
    values = { merchant_id = action.sqlite_insert.merchant.id, name = "Widget" }
  }

  action "sqlite_update" "move" {
    table = "merchants"
    set   = { region = "eu" }
    where = { id = action.sqlite_insert.merchant.id }
  }

  action "sqlite_delete" "drop_product" {
    depends_on = [action.sqlite_update.move]
    table      = "products"
    where      = { id = action.sqlite_insert.product.id }
  }

  output "merchant_id"   { value = action.sqlite_insert.merchant.id }
  output "merchant_name" { value = action.sqlite_insert.merchant.name }
  output "product_owner" { value = action.sqlite_insert.product.merchant_id }
  output "moved"         { value = action.sqlite_update.move.rows_affected }
  output "deleted"       { value = action.sqlite_delete.drop_product.rows_affected }
}

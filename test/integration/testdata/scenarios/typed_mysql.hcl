scenario "typed_mysql" {
  required_providers {
    mysql = { source = "builtin/mysql" }
  }

  variable "dsn" {}

  provider "mysql" {
    dsn = var.dsn
  }

  action "mysql_insert" "merchant" {
    table  = "merchants"
    values = { name = "O'Brien & Co", active = true, settings = { tier = "gold", seats = 3 } }
  }

  action "mysql_insert" "product" {
    table  = "products"
    values = { merchant_id = action.mysql_insert.merchant.id, name = "Widget", price = 9.99 }
  }

  action "mysql_update" "deactivate" {
    table = "merchants"
    set   = { active = false }
    where = { id = action.mysql_insert.merchant.id }
  }

  action "mysql_delete" "drop_product" {
    depends_on = [action.mysql_update.deactivate]
    table      = "products"
    where      = { id = action.mysql_insert.product.id }
  }

  output "merchant_id"   { value = action.mysql_insert.merchant.id }
  output "active"        { value = action.mysql_insert.merchant.active }
  output "tier"          { value = action.mysql_insert.merchant.settings.tier }
  output "price"         { value = action.mysql_insert.product.price }
  output "product_owner" { value = action.mysql_insert.product.merchant_id }
  output "deactivated"   { value = action.mysql_update.deactivate.rows_affected }
  output "deleted"       { value = action.mysql_delete.drop_product.rows_affected }
}

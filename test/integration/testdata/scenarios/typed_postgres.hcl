scenario "typed_postgres" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  provider "postgres" {
    dsn = var.dsn
  }

  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = "O'Brien & Co" }
  }

  action "postgres_insert" "product" {
    table  = "products"
    values = { merchant_id = action.postgres_insert.merchant.id, name = "Widget", price = 9.99 }
  }

  action "postgres_update" "move" {
    table = "merchants"
    set   = { region = "eu" }
    where = { id = action.postgres_insert.merchant.id }
  }

  action "postgres_delete" "drop_product" {
    depends_on = [action.postgres_update.move]
    table      = "public.products"
    where      = { id = action.postgres_insert.product.id }
  }

  output "merchant_id"     { value = action.postgres_insert.merchant.id }
  output "default_region"  { value = action.postgres_insert.merchant.region }
  output "created_at"      { value = action.postgres_insert.merchant.created_at }
  output "price"           { value = action.postgres_insert.product.price }
  output "product_owner"   { value = action.postgres_insert.product.merchant_id }
  output "moved"           { value = action.postgres_update.move.rows_affected }
  output "deleted"         { value = action.postgres_delete.drop_product.rows_affected }
}

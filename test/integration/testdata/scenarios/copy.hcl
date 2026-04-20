scenario "copy_demo" {
  description = "Bulk-load merchants via COPY (inline) and products from a CSV file"
  tags        = ["postgres", "copy", "bulk"]

  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {
    default = ""
  }

  provider "postgres" {
    dsn = var.dsn
  }

  # Inline CSV — small fixture, no external file needed.
  action "copy" "seed_merchants" {
    table   = "merchants"
    columns = ["name", "region"]
    format  = "csv"
    data    = <<-CSV
      Copy Corp,us
      Copy Ltd,eu
    CSV
  }

  # File-based CSV — the engine resolves the path relative to this
  # scenario file before the provider sees it, so it works from any
  # working directory.
  action "copy" "seed_products" {
    table   = "products"
    columns = ["merchant_id", "name", "price"]
    format  = "csv"
    header  = true
    file    = "../fixtures/products.csv"
  }

  output "merchants_copied" { value = action.copy.seed_merchants.rows_copied }
  output "products_copied"  { value = action.copy.seed_products.rows_copied }
}

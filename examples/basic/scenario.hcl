scenario "seed_dev" {
  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "database_url" {
    default = "postgres://localhost:5432/orchard?sslmode=disable"
  }

  variable "merchant_name" {
    default = "Acme Corp"
  }

  variable "region" {
    default = "us"
  }

  provider "postgres" {
    dsn = var.database_url
  }

  action "sql" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.merchant_name}', '${var.region}') RETURNING id, name, region"
  }

  action "sql" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.sql.create_merchant.id}, 'Widget', 9.99) RETURNING id, name"
  }

  output "merchant_id" {
    value = action.sql.create_merchant.id
  }

  output "merchant_name" {
    value = action.sql.create_merchant.name
  }

  output "product_id" {
    value = action.sql.create_product.id
  }
}

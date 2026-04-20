scenario "basic_merchant_and_product" {
  description = "Insert a merchant and a product linked by FK, capturing RETURNING outputs"
  tags        = ["postgres", "fk-chain"]

  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "dsn" {
    default = ""
  }

  variable "merchant_name" {
    default = "Acme Corp"
  }

  variable "region" {
    default = "us"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  action "postgres_query" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.merchant_name}', '${var.region}') RETURNING id, name, region"
  }

  action "postgres_query" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.postgres_query.create_merchant.id}, 'Widget', 9.99) RETURNING id, name"
  }

  output "merchant_id" {
    value = action.postgres_query.create_merchant.id
  }

  output "merchant_name" {
    value = action.postgres_query.create_merchant.name
  }

  output "product_id" {
    value = action.postgres_query.create_product.id
  }
}

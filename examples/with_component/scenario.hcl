scenario "merchant_with_products" {
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

  provider "postgres" {
    dsn = var.database_url
  }

  component "merchant" "base" {
    source = "../basic/components/merchant.hcl"

    inputs = {
      name = var.merchant_name
    }
  }

  action "sql" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${component.merchant.base.id}, 'Widget', 9.99) RETURNING id, name"
  }

  output "merchant_id" {
    value = component.merchant.base.id
  }

  output "product_id" {
    value = action.sql.create_product.id
  }
}

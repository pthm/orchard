scenario "test_scenario" {
  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "merchant_name" {
    default = "Acme Corp"
  }

  variable "region" {
    default = "us"
  }

  provider "postgres" {
    dsn = "postgres://localhost:5432/test"
  }

  action "sql" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.merchant_name}', '${var.region}') RETURNING id, name"
  }

  action "sql" "create_product" {
    query = "INSERT INTO products (merchant_id, name) VALUES (${action.sql.create_merchant.id}, 'Widget') RETURNING id"
  }

  output "merchant_id" {
    value = action.sql.create_merchant.id
  }
}

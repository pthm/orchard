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

  action "postgres_query" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.merchant_name}', '${var.region}') RETURNING id, name"
  }

  action "postgres_query" "create_product" {
    query = "INSERT INTO products (merchant_id, name) VALUES (${action.postgres_query.create_merchant.id}, 'Widget') RETURNING id"
  }

  output "merchant_id" {
    value = action.postgres_query.create_merchant.id
  }
}

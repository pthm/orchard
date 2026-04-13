scenario "test_scenario" {
  variable "merchant_name" {
    default = "Acme Corp"
  }

  variable "region" {
    default = "us"
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

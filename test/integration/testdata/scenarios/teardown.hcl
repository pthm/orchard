scenario "teardown_demo" {
  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "dsn" {
    default = ""
  }

  variable "merchant_name" {
    default = "Teardown Inc"
  }

  variable "region" {
    default = "us"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  # Both actions declare a lifecycle.teardown so `orchard teardown`
  # can unwind the FK chain in reverse order — product first, then
  # merchant, avoiding the FK constraint violation the opposite
  # order would hit.

  action "sql" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.merchant_name}', '${var.region}') RETURNING id, name"

    lifecycle {
      teardown {
        query = "DELETE FROM merchants WHERE id = ${self.id}"
      }
    }
  }

  action "sql" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.sql.create_merchant.id}, 'Teardown Widget', 9.99) RETURNING id, name"

    lifecycle {
      teardown {
        query = "DELETE FROM products WHERE id = ${self.id}"
      }
    }
  }

  output "merchant_id" { value = action.sql.create_merchant.id }
  output "product_id"  { value = action.sql.create_product.id }
}

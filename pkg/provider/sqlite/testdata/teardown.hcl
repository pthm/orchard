scenario "sqlite_teardown" {
  required_providers {
    sqlite = { source = "builtin/sqlite" }
  }

  variable "path" {}

  provider "sqlite" {
    path = var.path
  }

  # No RETURNING: the merchant's id comes back as last_insert_id.
  action "sqlite_query" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')"

    lifecycle {
      teardown {
        query = "DELETE FROM merchants WHERE id = ${self.last_insert_id}"
      }
    }
  }

  # RETURNING, and a foreign key to the merchant, so teardown must run
  # this action's cleanup first.
  action "sqlite_query" "create_product" {
    query = "INSERT INTO products (merchant_id, name) VALUES (${action.sqlite_query.create_merchant.last_insert_id}, 'Widget') RETURNING id, merchant_id"

    lifecycle {
      teardown {
        query = "DELETE FROM products WHERE id = ${self.id}"
      }
    }
  }

  output "merchant_id" { value = action.sqlite_query.create_merchant.last_insert_id }
  output "product_merchant_id" { value = action.sqlite_query.create_product.merchant_id }
}

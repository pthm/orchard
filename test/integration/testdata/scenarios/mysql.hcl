scenario "mysql_demo" {
  description = "Merchant and product on MySQL, wired by last_insert_id, with teardown"
  tags        = ["mysql", "teardown", "fk-chain"]

  required_providers {
    mysql = { source = "builtin/mysql" }
  }

  variable "dsn" {
    default = ""
  }

  provider "mysql" {
    dsn = var.dsn
  }

  # MySQL has no RETURNING: each insert's id comes back as
  # last_insert_id, and teardown deletes by it.
  action "mysql_query" "create_merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('Acme', 'us')"

    lifecycle {
      teardown {
        query = "DELETE FROM merchants WHERE id = ${self.last_insert_id}"
      }
    }
  }

  # The product references the merchant, so teardown must delete it
  # first.
  action "mysql_query" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.mysql_query.create_merchant.last_insert_id}, 'Widget', 9.99)"

    lifecycle {
      teardown {
        query = "DELETE FROM products WHERE id = ${self.last_insert_id}"
      }
    }
  }

  # A SELECT's first row becomes outputs, with columns decoded by type.
  action "mysql_query" "read_back" {
    query = "SELECT p.merchant_id, p.price, m.created_at FROM products p JOIN merchants m ON m.id = p.merchant_id WHERE p.id = ${action.mysql_query.create_product.last_insert_id}"
  }

  # Several statements in one query (multiStatements is always on).
  action "mysql_query" "move_region" {
    query = "START TRANSACTION; UPDATE merchants SET region = 'eu' WHERE id = ${action.mysql_query.read_back.merchant_id}; COMMIT;"
  }

  output "merchant_id"         { value = action.mysql_query.create_merchant.last_insert_id }
  output "product_id"          { value = action.mysql_query.create_product.last_insert_id }
  output "product_merchant_id" { value = action.mysql_query.read_back.merchant_id }
  output "price"               { value = action.mysql_query.read_back.price }
  output "created_at"          { value = action.mysql_query.read_back.created_at }
}

scenario "combined_providers" {
  description = "Cross-provider wiring: echo output flows into postgres, diamond FK dependency"
  tags        = ["postgres", "external", "cross-provider", "diamond"]

  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
    echo = {
      source = "exec:../echo/echo-provider"
    }
  }

  variable "dsn" {
    default = ""
  }

  variable "merchant_prefix" {
    default = "merch"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  provider "echo" {
    # echo declares no config
  }

  # Step 1: the external echo provider produces a pseudo-random name
  # whose value the SQL chain below depends on. This exercises the
  # cross-provider edge: echo output flows into a postgres INSERT.
  action "randomize" "merchant_name" {
    prefix = var.merchant_prefix
  }

  # Step 2: create the merchant using the randomize output. Everything
  # downstream FKs against this row.
  action "sql" "merchant" {
    query = "INSERT INTO merchants (name, region) VALUES ('${action.randomize.merchant_name.value}', 'us') RETURNING id, name"
  }

  # Steps 3a/3b: two sibling products under the same merchant. They
  # must run after #2 but can run in either order relative to each
  # other — a good test of topological freedom.
  action "sql" "product_a" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.sql.merchant.id}, 'Widget', 9.99) RETURNING id, name"
  }

  action "sql" "product_b" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${action.sql.merchant.id}, 'Gadget', 19.99) RETURNING id, name"
  }

  # Step 4: a review that FKs BOTH product_a and merchant — diamond
  # dependency. Only runnable after both upstream nodes complete.
  action "sql" "review" {
    query = "INSERT INTO product_reviews (product_id, merchant_id, rating, comment) VALUES (${action.sql.product_a.id}, ${action.sql.merchant.id}, 5, 'great widget') RETURNING id, rating"
  }

  output "merchant_id" {
    value = action.sql.merchant.id
  }

  output "merchant_name" {
    value = action.sql.merchant.name
  }

  output "product_a_id" {
    value = action.sql.product_a.id
  }

  output "product_b_id" {
    value = action.sql.product_b.id
  }

  output "review_id" {
    value = action.sql.review.id
  }
}

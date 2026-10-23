scenario "typed_teardown" {
  required_providers {
    sqlite = { source = "builtin/sqlite" }
  }

  variable "path" {}

  provider "sqlite" {
    path = var.path
  }

  # No lifecycle blocks: teardown deletes the product, then the merchant.
  action "sqlite_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme", region = "us" }
  }

  action "sqlite_insert" "product" {
    table  = "products"
    values = { merchant_id = action.sqlite_insert.merchant.id, name = "Widget" }
  }

  # Opted out: left in place.
  action "sqlite_insert" "kept" {
    table  = "merchants"
    values = { name = "Kept", region = "eu" }

    lifecycle {
      managed_teardown = false
    }
  }

  # An explicit teardown wins over the automatic delete. A typed
  # insert's teardown body is another insert, here into a log.
  action "sqlite_insert" "noted" {
    table  = "merchants"
    values = { name = "Noted", region = "us" }

    lifecycle {
      teardown {
        table  = "log"
        values = { msg = "noted ${self.id}" }
      }
    }
  }
}

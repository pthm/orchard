scenario "merchant_with_products" {
  description = "Component composition: merchant loaded from an external file, outputs wired into a product insert"
  tags        = ["postgres", "component", "composition"]

  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  variable "dsn" {
    default = ""
  }

  variable "merchant_name" {
    default = "Globex"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  component "merchant" "base" {
    source = "../components/merchant.hcl"

    inputs = {
      name = var.merchant_name
    }
  }

  action "postgres_query" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${component.merchant.base.id}, 'Gadget', 19.99) RETURNING id, name"
  }

  output "merchant_id" {
    value = component.merchant.base.id
  }

  output "merchant_name" {
    value = component.merchant.base.name
  }

  output "product_id" {
    value = action.postgres_query.create_product.id
  }
}

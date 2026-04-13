scenario "merchant_with_products" {
  variable "merchant_name" {
    default = "Globex"
  }

  component "merchant" "base" {
    source = "../components/merchant.hcl"

    inputs = {
      name = var.merchant_name
    }
  }

  action "sql" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${component.merchant.base.id}, 'Gadget', 19.99) RETURNING id, name"
  }

  output "merchant_id" {
    value = component.merchant.base.id
  }

  output "merchant_name" {
    value = component.merchant.base.name
  }

  output "product_id" {
    value = action.sql.create_product.id
  }
}

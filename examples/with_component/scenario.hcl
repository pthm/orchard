scenario "merchant_with_products" {
  variable "merchant_name" {
    default = "Acme Corp"
  }

  component "merchant" "base" {
    source = "../basic/components/merchant.hcl"

    inputs = {
      name = var.merchant_name
    }
  }

  action "sql" "create_product" {
    query = "INSERT INTO products (merchant_id, name, price) VALUES (${component.merchant.base.id}, 'Widget', 9.99) RETURNING id, name"
  }

  output "merchant_id" {
    value = component.merchant.base.id
  }

  output "product_id" {
    value = action.sql.create_product.id
  }
}

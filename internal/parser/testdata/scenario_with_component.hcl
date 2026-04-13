scenario "with_component" {
  variable "merchant_name" {
    default = "Acme Corp"
  }

  component "merchant" "base" {
    source = "./merchant_component.hcl"

    inputs = {
      name = var.merchant_name
    }
  }

  output "merchant_id" {
    value = component.merchant.base.id
  }
}

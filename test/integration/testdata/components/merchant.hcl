component "merchant" {
  description = "Insert a merchant row and expose its id, name, and region"
  tags        = ["postgres", "seed"]

  variable "name" {
    default = "Default Merchant"
  }

  variable "region" {
    default = "us"
  }

  action "sql" "create" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.name}', '${var.region}') RETURNING id, name, region"
  }

  output "id" {
    value = action.sql.create.id
  }

  output "name" {
    value = action.sql.create.name
  }

  output "region" {
    value = action.sql.create.region
  }
}

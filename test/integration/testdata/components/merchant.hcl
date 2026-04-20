component "merchant" {
  description = "Insert a merchant row and expose its id, name, and region"
  tags        = ["postgres", "seed"]

  variable "name" {
    default = "Default Merchant"
  }

  variable "region" {
    default = "us"
  }

  action "postgres_query" "create" {
    query = "INSERT INTO merchants (name, region) VALUES ('${var.name}', '${var.region}') RETURNING id, name, region"
  }

  output "id" {
    value = action.postgres_query.create.id
  }

  output "name" {
    value = action.postgres_query.create.name
  }

  output "region" {
    value = action.postgres_query.create.region
  }
}

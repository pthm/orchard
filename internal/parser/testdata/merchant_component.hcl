component "merchant" {
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
}

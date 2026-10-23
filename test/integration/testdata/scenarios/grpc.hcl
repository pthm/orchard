scenario "grpc" {
  required_providers {
    postgres = { source = "builtin/postgres" }
    accounts = { source = "builtin/grpc" }
  }

  variable "dsn" {}
  variable "address" {}
  variable "descriptor_set" { default = "" }
  variable "plan" { default = "PLAN_TEAM" }

  provider "postgres" {
    dsn = var.dsn
  }

  provider "accounts" {
    address        = var.address
    descriptor_set = var.descriptor_set == "" ? null : var.descriptor_set
  }

  action "grpc_call" "org" {
    method  = "accounts.v1.Accounts/CreateOrg"
    request = { name = "Acme", plan = var.plan, owner = { email = "admin@example.com", age = 41 } }
  }

  action "grpc_call" "get" {
    method  = "accounts.v1.Accounts/GetOrg"
    request = { org_id = action.grpc_call.org.response.org.id }
  }

  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = action.grpc_call.get.response.id }
  }

  output "org_id"      { value = action.grpc_call.org.response.org.id }
  output "owner_age"   { value = action.grpc_call.get.response.owner.age }
  output "merchant"    { value = action.postgres_insert.merchant.name }
  output "request_id"  { value = action.grpc_call.org.headers["x-request-id"] }
}

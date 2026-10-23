scenario "redis" {
  required_providers {
    postgres = { source = "builtin/postgres" }
    redis    = { source = "builtin/redis" }
  }

  variable "dsn" {}
  variable "redis_url" {}

  provider "postgres" {
    dsn = var.dsn
  }

  provider "redis" {
    url = var.redis_url
  }

  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }

  action "redis_set" "session" {
    key   = "session:${action.postgres_insert.merchant.id}"
    value = { merchant_id = action.postgres_insert.merchant.id, roles = ["admin"] }
    ttl   = "1h"
  }

  action "redis_hset" "flags" {
    key    = "flags:${action.postgres_insert.merchant.id}"
    fields = { new_checkout = true, limit = 10 }
  }

  action "redis_command" "enqueue" {
    args = ["RPUSH", "jobs:welcome", jsonencode({ merchant_id = action.postgres_insert.merchant.id })]
  }

  output "merchant_id"     { value = action.postgres_insert.merchant.id }
  output "session_key"     { value = action.redis_set.session.key }
  output "session_created" { value = action.redis_set.session.created }
  output "new_fields"      { value = action.redis_hset.flags.new_fields }
  output "queue_length"    { value = action.redis_command.enqueue.result }
}

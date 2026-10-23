scenario "redis_teardown" {
  required_providers {
    redis = { source = "builtin/redis" }
  }

  variable "redis_url" {}

  provider "redis" {
    url = var.redis_url
  }

  # Created: deleted on teardown.
  action "redis_set" "created" {
    key   = "created"
    value = "v1"
  }

  # Owned: restored to v1 before the key is deleted.
  action "redis_set" "owned" {
    depends_on = [action.redis_set.created]
    key        = "created"
    value      = "v2"
  }

  # Pre-existing, so restored only because it's asked for.
  action "redis_set" "requested" {
    key   = "requested"
    value = "ours"
    ttl   = "1h"

    lifecycle {
      managed_teardown = true
    }
  }

  # Pre-existing and not asked for: left as the scenario wrote it.
  action "redis_set" "unowned" {
    key   = "unowned"
    value = "ours"
  }

  # Pre-existing hash: only the field the scenario added is removed.
  action "redis_hset" "hash" {
    key    = "hash"
    fields = { a = "ours", b = "new" }
  }

  # Pre-existing hash, restored whole because it's asked for.
  action "redis_hset" "hash_requested" {
    key    = "hash_requested"
    fields = { a = "ours", b = "new" }

    lifecycle {
      managed_teardown = true
    }
  }
}

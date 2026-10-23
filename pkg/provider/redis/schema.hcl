provider "redis" {
  version  = "1"
  protocol = "1"

  description = "Redis, Valkey and other wire-compatible key-value stores."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - `redis_set` and `redis_hset` are typed alternatives to `redis_command` for the two
      shapes most seed data takes. Values are encoded the same way for both: strings
      as-is, numbers as decimal strings (`9.99`, `42`), bools as `true` / `false`, and
      objects, maps, lists and tuples as JSON. Null is an error.
    - `ttl` uses Go duration syntax (`"30s"`, `"1h"`), like the action-level `timeout`.
    - Each typed action reads what exists and writes in one `MULTI`/`EXEC`, so
      `created` and `new_fields` are exact even with other clients writing.
    - `redis_command` arguments are sent as text. Interpolated values aren't escaped
      or quoted, but they can't change which command runs: each list element is one
      argument.
  MD

  config {
    attribute "url" {
      type        = string
      required    = true
      description = <<-MD
        Connection URL: `redis://[user:password@]host:6379/<db>`, or `rediss://` for TLS.
        The path selects the logical database. Pass the password through a variable or
        `env()` rather than writing it into the scenario.
      MD
    }
  }

  action "redis_command" {
    description = "Send one command and return its reply."

    attribute "args" {
      type        = any
      required    = true
      description = "The command and its arguments, as a list of strings, numbers and bools. `jsonencode` structured values first."
    }

    output {
      type        = object({ result = any })
      description = <<-MD
        The reply as `result`:

        | Reply | Type |
        |-------|------|
        | simple, bulk or verbatim string | `string` |
        | integer, double, big number | `number` |
        | boolean | `bool` |
        | null | `null` |
        | array, set | tuple of converted elements |
        | map | object, keys as strings |

        An error reply fails the action.
      MD
    }

    example {
      description = "Push a job onto a queue."
      code        = <<-HCL
        action "redis_command" "enqueue" {
          args = ["LPUSH", "jobs:welcome_email", jsonencode({ user_id = action.postgres_insert.user.id })]
        }
      HCL
    }
  }

  action "redis_set" {
    description = <<-MD
      Store a value under a key (`SET`). With no `ttl`, any existing expiry is cleared,
      as `SET` does.
    MD

    attribute "key" {
      type        = string
      required    = true
      description = "The key."
    }

    attribute "value" {
      type        = any
      required    = true
      description = "The value. Objects, maps, lists and tuples are stored as JSON."
    }

    attribute "ttl" {
      type        = string
      description = "Expiry, as a duration like `\"1h\"`."
    }

    attribute "on_conflict" {
      type        = string
      default     = "update"
      description = "What to do when the key already exists: `\"update\"` overwrites it, `\"ignore\"` leaves it as it is (`SET NX`)."
    }

    output {
      type = object({
        key     = string,
        created = bool,
      })
      description = <<-MD
        | Field | Type | Description |
        |-------|------|-------------|
        | `key` | `string` | The key. |
        | `created` | `bool` | Whether the key was new. |
      MD
    }

    example {
      description = "Seed a session for a user, expiring in an hour."
      code        = <<-HCL
        action "redis_set" "session" {
          key   = "session:$${var.token}"
          value = { user_id = action.postgres_insert.user.id, roles = ["admin"] }
          ttl   = "1h"
        }
      HCL
    }
  }

  action "redis_hset" {
    description = <<-MD
      Set fields on a hash (`HSET`), then its expiry when `ttl` is set. With no `ttl`, an
      existing expiry is left alone, as `HSET` does.
    MD

    attribute "key" {
      type        = string
      required    = true
      description = "The key of the hash."
    }

    attribute "fields" {
      type        = any
      required    = true
      description = "Object of `field = value`."
    }

    attribute "ttl" {
      type        = string
      description = "Expiry for the whole hash, as a duration like `\"1h\"`."
    }

    attribute "on_conflict" {
      type        = string
      default     = "update"
      description = "What to do when the key already exists: `\"update\"` sets the fields on it, `\"ignore\"` leaves it as it is."
    }

    output {
      type = object({
        key        = string,
        created    = bool,
        new_fields = list(string),
      })
      description = <<-MD
        | Field | Type | Description |
        |-------|------|-------------|
        | `key` | `string` | The key. |
        | `created` | `bool` | Whether the hash was new. |
        | `new_fields` | `list(string)` | Fields the action added, sorted. Empty when nothing was written. |
      MD
    }

    example {
      description = "Turn on feature flags for an organisation."
      code        = <<-HCL
        action "redis_hset" "flags" {
          key    = "flags:$${action.postgres_insert.org.id}"
          fields = { new_checkout = true, beta_reports = false }
        }
      HCL
    }
  }
}

provider "demo" {
  version  = "1"
  protocol = "1"

  description = <<-MD
    Demo provider used exclusively by the docs renderer golden test.

    The schema exercises every field the renderer knows about — description,
    notes, provider-level examples, action-level examples, typed attributes
    with defaults, and outputs with rich markdown descriptions — so
    regressions in any one of them surface in the diff.
  MD

  notes = <<-MD
    - This provider is not real. Do not wire it into a scenario.
    - The fixture is intentionally concise; goldens should stay diffable.
  MD

  config {
    attribute "endpoint" {
      type        = string
      required    = true
      description = "Where the demo provider should pretend to connect."
    }
    attribute "timeout_seconds" {
      type        = number
      default     = 30
      description = "Per-request timeout."
    }
    attribute "tags" {
      type        = list(string)
      description = "Free-form tags the provider attaches to every call."
    }
  }

  action "ping" {
    description = "Pretend to ping the endpoint and return its round-trip time."

    attribute "payload" {
      type        = string
      default     = "hello"
      description = "Body written into the pretend ping."
    }

    output {
      type        = object({ rtt_ms = number, ok = bool })
      description = "Round-trip time and success flag."
    }

    example {
      description = "Minimal ping with the default payload."
      code        = <<-HCL
        action "ping" "health" {}
      HCL
    }

    notes = <<-MD
      Ping never actually touches the endpoint — it returns a fixed
      `rtt_ms = 0, ok = true` every call.
    MD
  }

  example {
    description = "Full scenario skeleton using the demo provider."
    code        = <<-HCL
      scenario "demo" {
        required_providers {
          demo = { source = "builtin/demo" }
        }

        provider "demo" {
          endpoint = "https://example.invalid"
        }

        action "ping" "health" {}
      }
    HCL
  }
}

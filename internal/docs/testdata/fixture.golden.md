---
title: demo
weight: 10
---

**Source:** `builtin/demo`

Demo provider used exclusively by the docs renderer golden test.

The schema exercises every field the renderer knows about — description,
notes, provider-level examples, action-level examples, typed attributes
with defaults, and outputs with rich markdown descriptions — so
regressions in any one of them surface in the diff.

## Provider configuration

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `endpoint` | `string` | yes | — | Where the demo provider should pretend to connect. |
| `tags` | `list(string)` | no | — | Free-form tags the provider attaches to every call. |
| `timeout_seconds` | `number` | no | `30` | Per-request timeout. |

## Actions

### `ping`

Pretend to ping the endpoint and return its round-trip time.

Minimal ping with the default payload.

```hcl
action "ping" "health" {}
```

**Attributes**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `payload` | `string` | no | `"hello"` | Body written into the pretend ping. |

**Outputs**

Type: `object({ok=bool,rtt_ms=number})`.

Round-trip time and success flag.

Ping never actually touches the endpoint — it returns a fixed
`rtt_ms = 0, ok = true` every call.

## Examples

Full scenario skeleton using the demo provider.

```hcl
scenario "demo" {
  required_providers {
    demo = { source = "builtin/demo" }
  }

  provider "demo" {
    endpoint = "https://example.invalid"
  }

  action "ping" "health" {}
}
```

## Notes

- This provider is not real. Do not wire it into a scenario.
- The fixture is intentionally concise; goldens should stay diffable.


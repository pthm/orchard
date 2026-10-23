---
title: Provider protocol
weight: 10
---

Orchard and its providers communicate using a line-oriented JSON protocol over
stdio. This page defines the wire format. If you're writing a provider, these are
the exact bytes you need to read and write.

## Framing: JSON Lines

Every message is a single JSON object on its own line, terminated by `\n`.
Readers split on `\n`, parse the line as one JSON value, and match it to a
request by id.

```
{"id":1,"method":"describe","params":{}}\n
{"id":1,"result":{"schema":{...}}}\n
```

Implementation notes:

- Do **not** pretty-print. Each frame is a single line.
- Do **not** emit framing bytes (length prefixes, headers). Just JSON + newline.
- Write frames atomically — if you share stdout across goroutines, serialize
  writes or you'll interleave messages.
- Flush stdout after each frame. Reader on the other end is waiting for `\n`.
- Write **logs to stderr, never stdout**. stdout is reserved for the protocol.

## Envelope

Orchard sends **requests**; the provider sends **responses** keyed by the same
id. Responses can arrive in any order; providers that process sequentially will
always respond in order.

### Request

```json
{
  "id": 42,
  "method": "describe",
  "params": { }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | `int64` | Unique id for this request. Provider echoes it back in the response. |
| `method` | `string` | One of `describe`, `configure`, `execute`, `shutdown`. |
| `params` | object | Method-specific payload. May be omitted if empty. |

### Response — success

```json
{
  "id": 42,
  "result": { "schema": { "name": "echo", ... } }
}
```

### Response — error

```json
{
  "id": 42,
  "error": { "code": "unknown_action", "message": "unknown action: foo" }
}
```

A response must contain **exactly one** of `result` or `error`.

### Error object

| Field | Type | Description |
|-------|------|-------------|
| `code` | `string` | One of the well-known codes below, or a provider-specific code. |
| `message` | `string` | Short human-readable message. |
| `detail` | `string` | Optional longer detail string. |

**Well-known error codes:**

| Code | Meaning |
|------|---------|
| `invalid_request` | The request is malformed or cannot be decoded. |
| `unknown_method` | The provider does not recognize `method`. |
| `unknown_action` | `execute` called with an action not in the provider's schema. |
| `validation_failed` | Action attributes fail the provider's runtime validation. |
| `configure_failed` | `configure` failed (bad credentials, connection refused, etc.). |
| `execute_failed` | `execute` failed (a runtime error while running the action). |
| `internal_error` | Provider-side bug or unexpected condition. |

Providers are free to define additional codes. Orchard reasons about the above
codes directly; others are treated as generic failures.

## Methods

See [execution model](../execution-model/) for the full lifecycle. In brief:

| Method | Params | Result | Called |
|--------|--------|--------|--------|
| `describe` | `{}` | `{ "schema": ProviderSchema }` | Once, at startup, before anything else. |
| `configure` | `{ "config": { ... } }` | `{}` | Once, in `run` mode only, before any `execute`. |
| `execute` | `{ "action": "name", "attrs": { ... } }` | `{ "outputs": { ... } }` | Once per action in the scenario. |
| `shutdown` | `{}` | `{}` | Once, at the end of the scenario. |

## Protocol versioning

Every provider schema must declare `"protocol": "1"`. Orchard checks this at
handshake time and refuses providers that advertise a mismatched version. There
is no negotiation; the version is either right or wrong.

`ProtocolVersion` is defined in `pkg/protocol/schema.go`. When it changes,
Orchard and providers must be upgraded together.

## The `schema` subcommand

For `orchard plan`, Orchard does not start the full RPC loop. Instead it runs:

```
<binary> schema
```

The binary must print the `ProviderSchema` as JSON to stdout and exit 0. Any
stderr output is surfaced to the user on failure. Implement this as a separate
code path from the main loop:

```go
func main() {
    if len(os.Args) > 1 && os.Args[1] == "schema" {
        writeSchemaJSON(os.Stdout)
        return
    }
    runRPCLoop()
}
```

Without a `schema` subcommand, `orchard plan` cannot validate your provider's
scenarios.

## Reference implementation

`pkg/protocol/` in the Orchard repo defines the envelope, methods, and schema
types. Go providers can import it directly. Providers in other languages
implement the protocol from scratch against the types documented here.

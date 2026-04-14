---
title: http
weight: 20
---

# http

HTTP request provider.

**Source:** `builtin/http`

## Provider configuration

```hcl
provider "http" {
  base_url = "https://api.example.com"
  default_headers = {
    Authorization = "Bearer ${var.api_token}"
  }
  timeout_seconds = 10
}
```

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `base_url` | `string` | no | — | Prepended to relative URLs in requests. When unset, URLs must be absolute. |
| `default_headers` | `map(string)` | no | — | Headers added to every request; per-request headers take precedence. |
| `timeout_seconds` | `number` | no | `30` | Per-request timeout in seconds applied to the full request/response round-trip. |

## Actions

### `request`

Issue an HTTP request and capture the response.

```hcl
action "request" "fetch_user" {
  method = "GET"
  url    = "/users/42"
}
```

**Attributes**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `method` | `string` | no | `"GET"` | HTTP method (GET, POST, PUT, PATCH, DELETE). |
| `url` | `string` | yes | — | Absolute URL, or relative path resolved against `base_url`. |
| `headers` | `map(string)` | no | — | Per-request headers, merged over `default_headers`. |
| `body` | `string` | no | — | Request body, sent as-is. Set the `Content-Type` header to control parsing on the server. |

**Outputs**

Type: `object({ status = number, body = string, headers = map(string) })`.

| Field | Type | Description |
|-------|------|-------------|
| `status` | `number` | HTTP status code (for example `200`, `404`). |
| `body` | `string` | Full response body, decoded as a string. |
| `headers` | `map(string)` | Response headers. Multi-valued headers are collapsed to the first value. |

## Complete example

```hcl
scenario "http_demo" {
  required_providers {
    http = { source = "builtin/http" }
  }

  provider "http" {
    base_url = "https://api.example.com"
    default_headers = {
      X-Client = "orchard"
    }
    timeout_seconds = 10
  }

  action "request" "create_user" {
    method = "POST"
    url    = "/users"
    headers = {
      Content-Type = "application/json"
    }
    body = jsonencode({ name = "Alice", email = "alice@example.com" })
  }

  action "request" "fetch_user" {
    url = "/users/${action.request.create_user.body}"
  }

  output "created_status" { value = action.request.create_user.status }
  output "fetched_body"   { value = action.request.fetch_user.body }
}
```

## Notes

- **URL resolution.** Relative URLs resolve against `base_url` via Go's
  `url.ResolveReference`. Absolute URLs (starting with `http://` or `https://`)
  bypass `base_url`.
- **Header merging.** For each request, `default_headers` is applied first, then
  per-request `headers`. Per-request values override defaults on key collision.
- **No retries.** The action fails on any transport error or non-2xx response
  interpretation — the provider simply returns whatever the server sent. Use
  the action-level `retry { }` block to retry on failure.
- **Response body.** The body is read entirely into memory and decoded as UTF-8.
  Large responses or binary content may be unsuitable.

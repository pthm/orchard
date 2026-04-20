provider "http" {
  version     = "1"
  protocol    = "1"
  description = "HTTP request provider."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
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
  MD

  config {
    attribute "base_url" {
      type        = string
      description = "Prepended to relative URLs in requests. When unset, URLs must be absolute."
    }
    attribute "default_headers" {
      type        = map(string)
      description = "Headers added to every request; per-request headers take precedence."
    }
    attribute "timeout_seconds" {
      type        = number
      default     = 30
      description = "Per-request timeout in seconds applied to the full request/response round-trip."
    }
  }

  action "http_request" {
    description = "Issue an HTTP request and capture the response."

    attribute "method" {
      type        = string
      default     = "GET"
      description = "HTTP method (GET, POST, PUT, PATCH, DELETE)."
    }
    attribute "url" {
      type        = string
      required    = true
      description = "Absolute URL, or relative path resolved against `base_url`."
    }
    attribute "headers" {
      type        = map(string)
      description = "Per-request headers, merged over `default_headers`."
    }
    attribute "body" {
      type        = string
      description = "Request body, sent as-is. Set the `Content-Type` header to control parsing on the server."
    }

    output {
      type = object({
        status  = number,
        body    = string,
        headers = map(string),
      })
      description = <<-MD
        Response status code, body, and headers.

        | Field | Type | Description |
        |-------|------|-------------|
        | `status` | `number` | HTTP status code (for example `200`, `404`). |
        | `body` | `string` | Full response body, decoded as a string. |
        | `headers` | `map(string)` | Response headers. Multi-valued headers are collapsed to the first value. |
      MD
    }

    example {
      description = "Simple GET against a relative path resolved via `base_url`."
      code        = <<-HCL
        action "http_request" "fetch_user" {
          method = "GET"
          url    = "/users/42"
        }
      HCL
    }
  }

  example {
    description = "POST a JSON payload, then fetch the created resource."
    code        = <<-HCL
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

        action "http_request" "create_user" {
          method = "POST"
          url    = "/users"
          headers = {
            Content-Type = "application/json"
          }
          body = jsonencode({ name = "Alice", email = "alice@example.com" })
        }

        action "http_request" "fetch_user" {
          url = "/users/$${action.http_request.create_user.body}"
        }

        output "created_status" { value = action.http_request.create_user.status }
        output "fetched_body"   { value = action.http_request.fetch_user.body }
      }
    HCL
  }
}

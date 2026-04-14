provider "http" {
  version     = "1"
  protocol    = "1"
  description = "HTTP request provider"

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
      description = "Per-request timeout in seconds."
    }
  }

  action "request" {
    description = "Issue an HTTP request and capture the response."

    attribute "method" {
      type        = string
      default     = "GET"
      description = "HTTP method (GET, POST, PUT, PATCH, DELETE)."
    }
    attribute "url" {
      type        = string
      required    = true
      description = "Absolute URL, or relative path resolved against base_url."
    }
    attribute "headers" {
      type        = map(string)
      description = "Per-request headers, merged over default_headers."
    }
    attribute "body" {
      type        = string
      description = "Request body, sent as-is. Set the Content-Type header to control parsing on the server."
    }

    output {
      type = object({
        status  = number,
        body    = string,
        headers = map(string),
      })
      description = "Response status code, body, and first value of each response header."
    }
  }
}

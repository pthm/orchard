scenario "http_roundtrip" {
  description = "HTTP GET and POST with base_url, default headers, and response capture"
  tags        = ["http", "headers", "post"]

  required_providers {
    http = {
      source = "builtin/http"
    }
  }

  variable "base_url" {
    default = ""
  }

  provider "http" {
    base_url = var.base_url
    default_headers = {
      X-Test-Suite = "integration"
    }
  }

  action "http_request" "fetch" {
    url = "/ping"
    headers = {
      X-Test-Case = "fetch"
    }
  }

  action "http_request" "submit" {
    method = "POST"
    url    = "/submit"
    body   = "{\"hello\":\"world\"}"
    headers = {
      Content-Type = "application/json"
    }
  }

  output "fetch_status" {
    value = action.http_request.fetch.status
  }

  output "fetch_body" {
    value = action.http_request.fetch.body
  }

  output "submit_status" {
    value = action.http_request.submit.status
  }
}

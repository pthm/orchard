scenario "http_roundtrip" {
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

  action "request" "fetch" {
    url = "/ping"
    headers = {
      X-Test-Case = "fetch"
    }
  }

  action "request" "submit" {
    method = "POST"
    url    = "/submit"
    body   = "{\"hello\":\"world\"}"
    headers = {
      Content-Type = "application/json"
    }
  }

  output "fetch_status" {
    value = action.request.fetch.status
  }

  output "fetch_body" {
    value = action.request.fetch.body
  }

  output "submit_status" {
    value = action.request.submit.status
  }
}

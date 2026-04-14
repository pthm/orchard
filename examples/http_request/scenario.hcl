scenario "http_demo" {
  required_providers {
    http = {
      source = "builtin/http"
    }
  }

  variable "base_url" {
    default = "https://httpbin.org"
  }

  provider "http" {
    base_url = var.base_url
    default_headers = {
      User-Agent = "orchard-example/1"
    }
  }

  action "request" "get_uuid" {
    url = "/uuid"
  }

  output "status" {
    value = action.request.get_uuid.status
  }

  output "body" {
    value = action.request.get_uuid.body
  }
}

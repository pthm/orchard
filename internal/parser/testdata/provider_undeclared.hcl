scenario "provider_undeclared" {
  required_providers {
    postgres = {
      source = "builtin/postgres"
    }
  }

  # "http" is not declared in required_providers — must error.
  provider "http" {
    base_url = "https://example.com"
  }
}

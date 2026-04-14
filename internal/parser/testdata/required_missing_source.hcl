scenario "required_missing_source" {
  required_providers {
    postgres = {
      version = "1.0"
      # source intentionally missing
    }
  }
}

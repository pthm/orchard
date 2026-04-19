scenario "echo_roundtrip" {
  description = "External echo provider: action chaining and string interpolation"
  tags        = ["external", "echo", "chaining"]

  required_providers {
    echo = {
      source = "exec:../echo/echo-provider"
    }
  }

  variable "greeting" {
    default = "hello from integration test"
  }

  provider "echo" {
    # echo provider declares no config attributes
  }

  action "echo" "first" {
    message = var.greeting
  }

  action "echo" "second" {
    message = "chained: ${action.echo.first.message}"
  }

  output "first" {
    value = action.echo.first.message
  }

  output "second" {
    value = action.echo.second.message
  }
}

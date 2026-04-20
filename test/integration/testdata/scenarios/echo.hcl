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

  action "echo_say" "first" {
    message = var.greeting
  }

  action "echo_say" "second" {
    message = "chained: ${action.echo_say.first.message}"
  }

  output "first" {
    value = action.echo_say.first.message
  }

  output "second" {
    value = action.echo_say.second.message
  }
}

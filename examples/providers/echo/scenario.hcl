scenario "echo_demo" {
  required_providers {
    echo = {
      # exec: resolves to a subprocess. Build the binary with:
      #   go build -o ./echo github.com/pthm/orchard/examples/providers/echo
      source = "exec:./echo"
    }
  }

  provider "echo" {
    # The echo provider declares no config attributes.
  }

  action "echo" "greeting" {
    message = "hello from orchard"
  }

  output "echoed" {
    value = action.echo.greeting.message
  }
}

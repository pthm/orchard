provider "exec" {
  version  = "1"
  protocol = "1"

  description = "Shell command execution"

  config {
    attribute "dir" {
      type        = string
      description = "Default working directory for commands (cwd-relative unless absolute)"
    }
    attribute "env" {
      type        = map(string)
      description = "Default environment variables merged into every action's env"
    }
  }

  action "run" {
    description = "Run a program and capture stdout/stderr/exit_code. Non-zero exit is an error by default."

    attribute "command" {
      type        = string
      required    = true
      description = "Program to run. Absolute path or PATH-resolved."
    }
    attribute "args" {
      type        = list(string)
      description = "Arguments passed after the command."
    }
    attribute "dir" {
      type        = string
      description = "Override the provider's default working directory for this action."
    }
    attribute "env" {
      type        = map(string)
      description = "Per-action environment variables. Merged over provider defaults; per-action wins on conflicts."
    }
    attribute "stdin" {
      type        = string
      description = "Data written to the process's stdin. Omit for no stdin."
    }

    output {
      type = object({
        stdout    = string,
        stderr    = string,
        exit_code = number,
      })
      description = "Captured output streams and exit code. Only produced on successful (exit 0) completion."
    }
  }
}

provider "exec" {
  version  = "1"
  protocol = "1"

  description = "Shell command execution."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - **Environment construction.** The child process's environment is built by
      starting from `os.Environ()`, layering the provider's `env`, then the action's
      `env`. Later layers win on conflicts.
    - **Working directory.** Per-action `dir` overrides the provider default. If
      neither is set, the child inherits the parent's cwd.
    - **Shell expansion.** `command` and `args` are passed directly to `exec.Command`
      — no shell is invoked, no glob expansion, no variable expansion. If you need a
      shell, run `sh -c "..."` explicitly:

      ```hcl
      action "run" "pipeline" {
        command = "sh"
        args    = ["-c", "ls | wc -l"]
      }
      ```

    - **Timeouts.** Use the action-level `timeout` attribute to bound execution:

      ```hcl
      action "run" "long_job" {
        timeout = "5m"
        command = "./slow-script.sh"
      }
      ```
  MD

  config {
    attribute "dir" {
      type        = string
      description = "Default working directory for commands. Relative paths are resolved against the process cwd."
    }
    attribute "env" {
      type        = map(string)
      description = "Default environment variables merged into every action's env."
    }
  }

  action "run" {
    description = <<-MD
      Run a program and capture stdout/stderr/exit_code.

      **Non-zero exit is an error by default** — the action fails and downstream
      actions don't run.
    MD

    attribute "command" {
      type        = string
      required    = true
      description = "Program to run. Absolute path or resolved via `PATH`."
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
      description = <<-MD
        Captured output streams and exit code. Only produced on successful
        (exit 0) completion; non-zero exit fails the action and surfaces the
        first 400 bytes of stderr in the error message.

        | Field | Type | Description |
        |-------|------|-------------|
        | `stdout` | `string` | Captured stdout. |
        | `stderr` | `string` | Captured stderr. |
        | `exit_code` | `number` | Process exit code (always `0` when outputs are produced). |
      MD
    }

    example {
      description = "Run a program with explicit arguments."
      code        = <<-HCL
        action "run" "migrate" {
          command = "psql"
          args    = ["-f", "migrations/001.sql"]
        }
      HCL
    }
  }

  example {
    description = "Build an artefact and capture its SHA256."
    code        = <<-HCL
      scenario "build_and_upload" {
        required_providers {
          exec = { source = "builtin/exec" }
        }

        provider "exec" {
          dir = "."
          env = {
            CI = "true"
          }
        }

        action "run" "build" {
          command = "go"
          args    = ["build", "-o", "bin/app", "./cmd/app"]
        }

        action "run" "hash" {
          depends_on = [action.run.build]
          command    = "shasum"
          args       = ["-a", "256", "bin/app"]
        }

        output "checksum" { value = action.run.hash.stdout }
      }
    HCL
  }
}

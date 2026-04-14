---
title: exec
weight: 30
---

# exec

Shell command execution.

**Source:** `builtin/exec`

## Provider configuration

```hcl
provider "exec" {
  dir = "/path/to/project"
  env = {
    PATH = "/usr/local/bin:/usr/bin"
  }
}
```

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `dir` | `string` | no | — | Default working directory for commands. Relative paths are resolved against the process cwd. |
| `env` | `map(string)` | no | — | Default environment variables merged into every action's env. |

## Actions

### `run`

Run a program and capture stdout/stderr/exit_code. **Non-zero exit is an error by
default** — the action fails and downstream actions don't run.

```hcl
action "run" "migrate" {
  command = "psql"
  args    = ["-f", "migrations/001.sql"]
}
```

**Attributes**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `command` | `string` | yes | — | Program to run. Absolute path or resolved via `PATH`. |
| `args` | `list(string)` | no | — | Arguments passed after the command. |
| `dir` | `string` | no | — | Override the provider's default working directory for this action. |
| `env` | `map(string)` | no | — | Per-action environment variables. Merged over provider defaults; per-action wins on conflicts. |
| `stdin` | `string` | no | — | Data written to the process's stdin. Omit for no stdin. |

**Outputs**

Type: `object({ stdout = string, stderr = string, exit_code = number })`.

| Field | Type | Description |
|-------|------|-------------|
| `stdout` | `string` | Captured stdout. |
| `stderr` | `string` | Captured stderr. |
| `exit_code` | `number` | Process exit code (always `0` when outputs are produced — see below). |

Outputs are only produced on successful (exit 0) completion. If the command exits
non-zero, the action fails and stderr is included in the error message (truncated
to 400 bytes).

## Complete example

```hcl
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
```

## Notes

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

---
title: Installation
weight: 10
---

# Installation

Orchard ships as a single Go binary. There is no daemon, no server, and no database
of its own — it reads HCL files and talks to the systems you configure.

## Install from source

```sh
go install github.com/pthm/orchard@latest
```

This installs `orchard` into `$GOBIN` (defaulting to `$HOME/go/bin`). Make sure that
directory is on your `PATH`.

Verify the install:

```sh
orchard --help
```

You should see the top-level command with `run`, `plan`, and `list` subcommands.

## Requirements

- **Go 1.22 or newer** if installing from source.
- **A target system to drive.** Orchard itself is just an orchestrator — you need
  something for it to talk to. For the built-in providers that means a reachable
  PostgreSQL database (for `builtin/postgres`), an HTTP endpoint (for `builtin/http`),
  or a shell environment (for `builtin/exec`).

## Prebuilt binaries

Prebuilt binaries are not yet published. Until then, `go install` is the supported
path.

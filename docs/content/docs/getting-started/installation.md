---
title: Installation
weight: 10
---

Orchard ships as a single Go binary. There is no daemon, no server, and no database
of its own — it reads HCL files and talks to the systems you configure.

## Homebrew

On macOS and Linux:

```sh
brew install pthm/tap/orchard
```

## Scoop

On Windows:

```sh
scoop bucket add pthm https://github.com/pthm/scoop-bucket
scoop install pthm/orchard
```

## Install with Go

```sh
go install github.com/pthm/orchard@latest
```

This installs `orchard` into `$GOBIN` (defaulting to `$HOME/go/bin`). Make sure that
directory is on your `PATH`.

## Prebuilt binaries

Each [GitHub release](https://github.com/pthm/orchard/releases) includes:

- `.tar.gz` archives for Linux and macOS, and `.zip` archives for Windows (amd64 and arm64)
- `.deb` and `.rpm` packages for Linux
- `checksums.txt` with SHA-256 sums for every artifact

Download the file for your platform, check it against `checksums.txt`, and put the
`orchard` binary on your `PATH` (or install the package with `dpkg -i` / `rpm -i`).

## Verify the install

```sh
orchard version
```

This prints the installed version, commit, and build date. Run `orchard --help` to
see the available subcommands.

## Requirements

- **Go 1.25 or newer** if installing with `go install`.
- **A target system to drive.** Orchard itself is just an orchestrator — you need
  something for it to talk to. For the built-in providers that means a reachable
  PostgreSQL database (for `builtin/postgres`), an HTTP endpoint (for `builtin/http`),
  or a shell environment (for `builtin/exec`).

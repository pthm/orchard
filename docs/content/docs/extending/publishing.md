---
title: Publishing a provider
linkTitle: Publishing
weight: 50
---

Once your provider works, users need a way to install and use it. Today that
means: give them a binary and have them reference it by path. A registry is
future work.

## Consumption model

Scenarios reference providers by source string:

```hcl
required_providers {
  stripe = { source = "exec:./bin/orchard-stripe" }
}
```

The path is resolved **relative to the scenario file that declares the
provider**. Absolute paths work too. The binary must be executable.

There is no auto-discovery from `$PATH`, no version pinning, and no registry
today. The source string points at one specific binary.

## Packaging

### For a single team

The simplest approach: commit the provider source to the same repo as the
scenarios that use it, add a Makefile target that builds the binary, and
have developers run `make providers` before `orchard run`.

```
my-project/
├── Makefile
├── providers/
│   └── stripe/
│       ├── go.mod
│       └── main.go
├── bin/
│   └── orchard-stripe    # built, gitignored
└── scenarios/
    └── demo.hcl          # source = "exec:../bin/orchard-stripe"
```

```makefile
.PHONY: providers
providers:
	cd providers/stripe && go build -o ../../bin/orchard-stripe .
```

### For cross-team distribution

Publish a release (GitHub Releases, an internal artifact repo) with per-platform
binaries:

```
orchard-stripe-0.3.0-darwin-arm64
orchard-stripe-0.3.0-darwin-amd64
orchard-stripe-0.3.0-linux-amd64
orchard-stripe-0.3.0-linux-arm64
```

Consumers pin a version, download the binary to a known location, and reference
it from their scenarios:

```hcl
required_providers {
  stripe = { source = "exec:./vendor/orchard-providers/stripe" }
}
```

A `make install` or shell script that downloads and vendors the right binary
keeps this repeatable across machines.

## Versioning

- **Use semver** for the provider's `version` field in its schema.
- **Bump the major version** when you make breaking changes to action names,
  required attributes, or output shapes.
- **Protocol version** (the `protocol` field) should stay at `"1"` unless
  Orchard itself ships a new protocol version. It tracks the wire protocol,
  not the provider's own API.

Orchard does not currently enforce version constraints — a scenario cannot
declare "stripe ≥ 0.3.0". Users who need constraint checking should build it
into their packaging layer (e.g. a script that refuses to install a binary
whose schema reports the wrong version).

## Testing before publishing

A published provider should pass at least:

1. **Schema probe.** `./orchard-stripe schema | jq .` produces a valid
   schema JSON document.
2. **Plan against a scenario.** `orchard plan scenario.hcl` succeeds without
   configure/execute errors.
3. **Run against a real target.** `orchard run scenario.hcl` against a
   sandbox / test account.
4. **Shutdown.** The process exits cleanly within 5 seconds of the shutdown
   RPC. Check with `orchard run` and watch for leftover processes.

A small Go test that spawns the binary and drives it through a full
describe/configure/execute/shutdown handshake will catch most regressions.
See `pkg/provider/external/external_test.go` in the Orchard repo for the
in-tree equivalent.

## Roadmap

A future release will add:

- **A registry.** Providers published by name (e.g. `stripe` instead of
  `exec:./bin/orchard-stripe`), with signed releases and version resolution.
- **Version constraints.** `required_providers { stripe = { source = "...", version = ">= 0.3" } }`.
- **Checksum pinning.** Trusted binary verification like Terraform's
  `.terraform.lock.hcl`.

Until then, treat external providers the way you'd treat any other vendored
binary dependency: pick a distribution mechanism, pin a version, and be
explicit about where the file lives.

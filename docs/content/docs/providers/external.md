---
title: External providers
weight: 40
---

# External providers

External providers are binaries that live outside the `orchard` tree and speak
Orchard's [provider protocol](../../extending/provider-protocol/) over stdio.
Scenarios use them exactly like built-in providers — the only difference is the
source string.

If you want to **build** a custom provider, see
[Extending Orchard](../../extending/). This page covers how to **use** an
existing external provider from a scenario.

## Declaring an external provider

```hcl
required_providers {
  stripe = { source = "exec:./bin/orchard-stripe" }
}
```

The source format is `exec:<path>`, where `<path>` is an executable file:

- Relative paths are resolved against the directory of the scenario file that
  declares the provider.
- Absolute paths are used as-is.
- The binary must be executable (`chmod +x`).

## Configuration and actions

Once declared, an external provider behaves like any other provider:

```hcl
provider "stripe" {
  api_key = var.stripe_api_key
}

action "charge" "test_charge" {
  amount   = 1000
  currency = "usd"
  customer = "cus_123"
}
```

The available config attributes and action types come from the provider's schema,
which Orchard fetches at plan time by running `<binary> schema`.

## Plan versus run

The two modes treat external providers differently:

- **`orchard plan`** runs `<binary> schema`, reads the schema JSON, and exits.
  The provider subprocess is short-lived and never accepts configure or execute
  calls. No credentials are needed to plan.
- **`orchard run`** starts the provider as a long-lived subprocess, performs a
  describe/configure handshake, issues execute calls for each action, and sends
  a shutdown when the scenario finishes.

This split is what makes `plan` safe to run against scenarios that point at
production systems — the provider binary is never handed credentials or asked
to do anything stateful.

See [plan vs run](../../concepts/plan-vs-run/) for the full picture.

## Troubleshooting

- **`external schema <path>: exit status 1 (stderr: ...)`** — the binary failed
  to produce a valid schema. Run `<binary> schema` directly and inspect output.
- **`external schema <path>: invalid: protocol version "..." not supported`** —
  the provider speaks a protocol version this `orchard` binary does not. Update
  one or the other.
- **Process hangs during run** — the provider subprocess's stderr is streamed
  to `orchard`'s logger and prefixed with the provider name. Look there for
  clues. If shutdown hangs, Orchard force-kills the process after 5 seconds.

## Discovery

Orchard does **not** auto-discover external providers from `$PATH` or a registry
today. You must reference them by path in `required_providers`. A registry /
discovery mechanism is future work.

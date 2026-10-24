---
title: Plan vs run
weight: 50
---

Orchard has two execution modes. They share parsing, validation, and graph
construction but diverge on how they deal with providers and side effects.

## `orchard plan`

`orchard plan` is a **read-only** preview. It:

1. Parses the scenario file and any component files it references.
2. Loads provider schemas.
3. Validates the scenario: every reference resolves, every required attribute
   is set, every type matches.
4. Builds and sorts the dependency graph.
5. Prints the plan — what will run, in what order, with what inputs.

`orchard plan --show-values` also lists each action's evaluated attributes;
values that come from other actions show as `(known after run)`.

`plan` **never configures a provider and never executes an action.** It cannot
open database connections, make HTTP requests, or spawn subprocesses. For
built-in providers, plan skips `Configure` entirely. For external providers,
plan uses a short-lived schema probe that runs `<binary> schema`, reads the
schema JSON, and exits immediately.

This is a deliberate design choice: plan should be fast, deterministic, and
side-effect-free. You can run it in CI against a scenario that points at
production without touching production.

## `orchard run`

`orchard run` executes the scenario. It:

1. Does everything `plan` does.
2. Calls `Configure` on each provider with its evaluated configuration.
3. Executes each action once its dependencies have completed (up to
   `--parallelism` at a time, default 1), passing evaluated attributes and
   capturing outputs.
4. Resolves scenario outputs and prints them.
5. Calls `Shutdown` on each provider.

For external providers, `run` starts a long-lived subprocess per provider and
keeps it alive until the scenario finishes. Communication happens over stdio
in JSON Lines.

## Why split them

The split mirrors Terraform's plan/apply split, for the same reason: a plan
should be cheap and safe enough to run constantly. Bundling describe and
configure into a single handshake would force every plan invocation to supply
real credentials and open real connections — which is both slow and risky.

In Orchard's case the split also makes external providers easier to write. A
provider binary only needs to implement two things to support plan:

- Respond to `<binary> schema` by printing a JSON schema.
- (For run) Implement the full stdio protocol.

See [provider protocol](../../extending/provider-protocol/) for details.

## When to use each

- **Run `plan` freely.** In CI, before merge, whenever you want to check
  scenario validity. It's cheap and safe.
- **Run `run` deliberately.** It modifies real systems. Treat it like any
  other side-effectful command.

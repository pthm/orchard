---
title: Extending Orchard
weight: 40
---

Orchard's built-in providers (`postgres`, `http`, `exec`) cover a lot of common
ground, but they won't cover everything. When you need to drive a system Orchard
doesn't know about — a SaaS API, an internal service, a queue, a vault — you
write a **custom provider**.

A custom provider is a binary that speaks Orchard's wire protocol over stdio.
It can be written in any language that can read and write JSON on stdin/stdout.
The `orchard` binary launches it as a subprocess, performs a short handshake,
and issues action calls.

## Pages in this section

- **[Provider protocol](provider-protocol/)** — the wire format: JSON Lines framing,
  request/response envelope, methods, error codes.
- **[Execution model](execution-model/)** — the lifecycle of a provider subprocess:
  describe, configure, execute, shutdown. How `plan` and `run` differ.
- **[Schema and types](schema-and-types/)** — the schema model: what a provider
  advertises, how types are expressed, how defaults work.
- **[Writing a provider](writing-a-provider/)** — end-to-end tutorial in Go,
  covering the stdio loop, method dispatch, schema declaration, and testing.
- **[Publishing](publishing/)** — packaging a provider binary, how users consume
  it, versioning guidance.

## When to write a custom provider

Write one when:

- The target system needs operations not expressible in SQL, HTTP, or shell.
- You want strong schema validation for domain-specific inputs ("a merchant",
  "a subscription").
- You want plan-time type checking for scenario authors — which a custom provider
  gets for free through its schema.

Skip writing a provider when:

- A shell command or HTTP call would do the job. `builtin/exec` and `builtin/http`
  are generic by design.
- The work is one-off. A component with a few built-in actions is simpler than a
  new provider.

## Before you start

Read [Concepts → Providers](../concepts/providers/) first if you haven't already.
The custom-provider work makes more sense once the built-in provider mechanics
are familiar.

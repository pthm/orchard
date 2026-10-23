---
title: Providers
weight: 2
cascade:
  type: docs
---

Reference documentation for each built-in provider. Every page documents the
provider's source string, its configuration schema, and every action it supports.

- [`postgres`](postgres/) — PostgreSQL data operations
- [`http`](http/) — HTTP request provider
- [`exec`](exec/) — Shell command execution
- [External providers](external/) — using custom providers from separate binaries

To build your own provider, see [Extending Orchard](/docs/extending/).

## Reading a provider page

Each page follows the same layout:

- **Source** — the string you put in `required_providers`.
- **Provider configuration** — every attribute accepted by the `provider "name" { }`
  block, with type, default, and whether it's required.
- **Actions** — one section per action type the provider handles, documenting
  attributes, outputs, and runtime behavior.
- **Example** — a complete, working scenario fragment you can adapt.

Types are written using Orchard's HCL type expressions: `string`, `number`, `bool`,
`list(string)`, `map(string)`, `object({ key = type, ... })`, or `any`. See
[expressions](/reference/expressions/) for the full type grammar.

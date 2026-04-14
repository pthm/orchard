---
title: Orchard
type: docs
---

# Orchard

**Define reproducible application state as code.**

Orchard is a CLI that lets teams define, compose, and execute *scenarios* — declarative
descriptions of complex application state — using HCL. Instead of writing ad-hoc seeding
scripts, teams describe what state they want and Orchard builds it: inserting database
rows, calling APIs, running scripts, and wiring outputs between steps automatically.

{{% columns %}}
- ### Scenario language, not fixture language
  Describe the state you want — a merchant with three failed payments, an org with SSO
  enabled and a suspended user — and Orchard composes the rows, calls, and side effects
  that produce it.

- ### Reproducible by design
  Same inputs, same state. Components are reusable building blocks; scenarios compose
  them into complete environments you can stand up, tear down, and share.
{{% /columns %}}

## Start here

- [Getting started](docs/getting-started/) — install the CLI and run your first scenario
- [Concepts](docs/concepts/) — scenarios, components, providers, and how they fit together
- [Reference](docs/reference/) — HCL schema, built-in providers, CLI flags

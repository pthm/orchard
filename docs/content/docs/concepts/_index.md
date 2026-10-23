---
title: Concepts
weight: 20
---

Orchard has a small vocabulary. Learn these terms and the rest of the system
follows naturally.

- **[Scenario](scenarios/)** — a top-level, runnable description of a desired state.
- **[Component](components/)** — a reusable unit of state that scenarios compose.
- **[Provider](providers/)** — the runtime that turns a declaration into side effects
  (Postgres rows, HTTP calls, shell commands).
- **[Wiring](wiring/)** — how outputs from one action flow into the inputs of the next,
  and how Orchard derives the execution order.
- **[Plan vs run](plan-vs-run/)** — the two execution modes, why they exist, and what
  they guarantee.
- **[Lifecycle and teardown](lifecycle/)** — run records, `lifecycle { teardown { } }`
  blocks, and `orchard teardown`.

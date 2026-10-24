---
title: Execution model
weight: 20
---

A provider is a process. Orchard starts it, talks to it, and stops it. This page
documents that lifecycle in detail so you know exactly what your provider code
has to handle.

## One process per scenario

In `run` mode, Orchard spawns one provider subprocess per `provider` declaration
in the scenario. The process stays alive for the duration of the scenario,
handles all actions routed to that provider, and receives `shutdown` at the end.

```
orchard run
  │
  ├── spawn postgres provider  ──→  process stays alive for scenario
  │    ├── describe
  │    ├── configure
  │    ├── execute (sql "create_merchant")
  │    ├── execute (sql "create_product")
  │    └── shutdown
  │
  └── spawn stripe provider    ──→  process stays alive for scenario
       ├── describe
       ├── configure
       ├── execute (charge "test_charge")
       └── shutdown
```

If a scenario declares `provider "postgres" {}` twice with different aliases,
**two processes** are spawned. Each one is independent — no shared state between
instances of the same provider.

## Lifecycle phases

### 1. Spawn

Orchard launches the binary with no arguments and opens:

- `stdin` — request pipe. Orchard writes JSON-lines requests here.
- `stdout` — response pipe. Provider writes JSON-lines responses here.
- `stderr` — log pipe. Everything the provider writes here is prefixed with the
  provider name and streamed to Orchard's logger.

### 2. Describe

Immediately after spawn, Orchard sends:

```json
{"id":1,"method":"describe","params":{}}
```

Within 5 seconds, the provider must respond with its schema:

```json
{"id":1,"result":{"schema":{"name":"echo","version":"1","protocol":"1","actions":{...}}}}
```

If the provider doesn't respond in time, Orchard force-kills it and fails the
scenario. The 5-second budget is a safety net for provider crashes and
deadlocks, not a performance target — describe should return in milliseconds.

Orchard validates the schema (required fields, valid types, matching protocol
version) before proceeding.

### 3. Configure

Once the schema is validated, Orchard evaluates the `provider "name" { }` block
from the scenario against the declared config schema, then sends:

```json
{"id":2,"method":"configure","params":{"config":{"dsn":"\"postgres://...\""}}}
```

The values in `config` are JSON-encoded cty values — see
[schema and types](../schema-and-types/#cty-values-on-the-wire). Every declared
config attribute is present; attributes with defaults are filled in even if the
scenario omitted them.

The provider opens connections, validates credentials, and generally prepares
to do work. On success:

```json
{"id":2,"result":{}}
```

On failure (bad credentials, unreachable host), the provider returns
`configure_failed` and the scenario aborts.

### 4. Execute (repeated)

For each action routed to this provider, Orchard sends:

```json
{"id":3,"method":"execute","params":{"action":"sql","attrs":{"query":"\"INSERT ...\""}}}
```

The provider runs the action and returns outputs:

```json
{"id":3,"result":{"outputs":{"id":"42","name":"\"Acme\""}}}
```

Orchard sends one request at a time to each provider process. With
`orchard run --parallelism N`, independent actions run concurrently across
providers, but actions routed to the same external provider queue and are
sent one after another.

### 5. Shutdown

When the scenario finishes (success or failure), Orchard sends:

```json
{"id":99,"method":"shutdown","params":{}}
```

The provider should flush, close connections, and respond:

```json
{"id":99,"result":{}}
```

It then has a few seconds to exit cleanly. If it doesn't, Orchard force-kills
the process.

## Plan mode

`orchard plan` does **not** follow the lifecycle above. Instead it runs:

```
<binary> schema
```

The binary prints the `ProviderSchema` as JSON to stdout, exits 0, and that's
it. No RPC loop, no configure, no execute. Plan uses the schema to type-check
references in the scenario and prints the execution graph; it never touches
external state.

This split means your provider needs **two entry points**:

1. A `schema` subcommand that dumps the schema and exits.
2. The default RPC loop for `run` mode.

See [writing a provider](../writing-a-provider/) for a worked example.

## Concurrency within a provider

Orchard calls one method at a time per provider process, even with
`--parallelism`. You don't need to handle concurrent requests. That said, the
protocol is async-capable (responses are id-matched) — if a future Orchard
version issues parallel executes to the same provider, your dispatch loop
should be prepared.

A simple, safe implementation: single-threaded read-request → dispatch →
write-response loop. That's what the reference implementations do.

## Failure modes

| Situation | Orchard's behavior |
|-----------|--------------------|
| Describe times out (5s) | Force-kill, fail scenario. |
| Describe returns invalid schema | Fail scenario with a validation error. |
| Configure errors | Fail scenario. Subsequent actions don't run. |
| Execute errors | Action fails. If the action has `retry { }`, retry per policy. Otherwise fail the scenario. |
| Provider crashes mid-execute | Scenario fails with a broken-pipe error. |
| Shutdown times out (5s) | Force-kill. Scenario result is not affected. |
| Provider writes garbage to stdout | Orchard fails to decode the frame; the next request will time out and the scenario fails. |

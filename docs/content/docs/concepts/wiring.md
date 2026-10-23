---
title: Wiring
weight: 40
---

**Wiring** is how outputs from one step flow into the inputs of the next, and how
Orchard decides the order to execute steps in. You never write a graph by hand —
you write references in expressions, and Orchard derives the graph from them.

## References

Four reference namespaces are in scope inside expressions:

| Form | Resolves to | Available when |
|------|-------------|----------------|
| `var.<name>` | A scenario or component variable. | Plan time — variables are resolved up front. |
| `action.<type>.<name>.<output>` | An output from another action. | After that action completes. |
| `component.<type>.<name>.<output>` | An output from a component instance. | After that component resolves. |
| `var.<name>` (inside a component) | The component's own variable. | As soon as the component runs. |

References can appear inside any attribute expression — in action bodies, provider
configs (variables only), output values, or component inputs.

## Implicit dependencies

When you reference `action.postgres_query.create_merchant.id` inside another action's body,
Orchard:

1. Parses the expression.
2. Extracts the traversal — "this block depends on `action.postgres_query.create_merchant`".
3. Adds an edge to the dependency graph.
4. Executes `create_merchant` first, captures its output, and evaluates the
   dependent expression with the value in scope.

You never declare this edge explicitly. This is the **primary** way dependencies
are expressed in Orchard: through data references. If you find yourself writing a
lot of explicit ordering, consider whether your actions should be passing data
instead.

## Explicit dependencies

Sometimes two actions are ordered by side effect rather than data — an HTTP call
that must happen before a SQL insert, with nothing flowing between them. Use
`depends_on`:

```hcl
action "http_request" "provision_tenant" {
  url    = "/provision"
  method = "POST"
  body   = "..."
}

action "postgres_query" "seed_tenant_data" {
  depends_on = [action.http_request.provision_tenant]
  query      = "INSERT INTO tenant_settings ..."
}
```

`depends_on` takes a list of references to other nodes. It adds graph edges
without pulling any data into scope.

## Repetition

`count` and `for_each` create several instances of an action or component from
one block.

```hcl
action "postgres_query" "payment" {
  count = 3
  query = "INSERT INTO payments (status) VALUES ('failed') RETURNING id"
}

component "merchant" "site" {
  source   = "./components/merchant.hcl"
  for_each = { eu = "Europe", us = "America" }
  inputs   = { name = "${each.key} (${each.value})" }
}
```

- `count` exposes `count.index` (0-based). `for_each` takes a map, object, or
  list/set of strings and exposes `each.key` and `each.value` (for a list,
  both are the element).
- Reference one instance by index or key: `action.postgres_query.payment[0].id`,
  `component.merchant.site["eu"].id`. Use a splat for all of them:
  `action.postgres_query.payment[*].id`.
- Anything that references the block waits for **every** instance.
- `count` and `for_each` must be known before anything runs, so they may only
  reference `var.*`, literals, and functions — not action or component outputs.
- An action can't reference its own instances, and `count`/`for_each` can't be
  set on actions inside a component file. Repeat the component instead.
- `count = 0` creates nothing. Referencing a block with zero instances is an
  error.

Instances appear in plans and run records as `payment[0]`, `site["eu"]`.
Teardown blocks can use `count.index` / `each.*` too.

## Dependency resolution

Orchard builds a directed acyclic graph of all actions and components in the
scenario, topologically sorts it, and executes nodes in order. Independent
branches run sequentially today; parallel execution is a future addition.

Cycles are detected at plan time and reported as errors — you cannot have two
actions that depend on each other.

## Output capture

Each provider decides what the outputs of its action look like. For example:

- The `postgres_query` action returns the first row of the result set as an object, keyed
  by column name.
- The `http_request` action returns an object with `status`, `body`, and `headers`.
- The `exec_run` action returns an object with `stdout`, `stderr`, and `exit_code`.

Downstream expressions access these as ordinary HCL attribute traversals:

```hcl
action "postgres_query" "next" {
  query = "SELECT * FROM users WHERE id = ${action.postgres_query.previous.user_id}"
}
```

See the [provider reference](/providers/) for each provider's exact output
shape.

## Advanced action attributes

Actions support a few attributes that are handled by the engine rather than passed
to the provider:

- **`provider = "name"`** — explicitly route the action to a named provider.
- **`depends_on = [ ... ]`** — add explicit dependency edges.
- **`count` / `for_each`** — create several instances. See [Repetition](#repetition).
- **`timeout = "30s"`** — wrap the provider's execute call in a Go-duration
  timeout. Exceeding the timeout fails the action.
- **`retry { }`** — retry the action on failure with backoff. See
  [HCL schema reference](../../reference/hcl-schema/#retry).

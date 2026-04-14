---
title: Wiring
weight: 40
---

# Wiring

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

When you reference `action.sql.create_merchant.id` inside another action's body,
Orchard:

1. Parses the expression.
2. Extracts the traversal — "this block depends on `action.sql.create_merchant`".
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
action "request" "provision_tenant" {
  url    = "/provision"
  method = "POST"
  body   = "..."
}

action "sql" "seed_tenant_data" {
  depends_on = [action.request.provision_tenant]
  query      = "INSERT INTO tenant_settings ..."
}
```

`depends_on` takes a list of references to other nodes. It adds graph edges
without pulling any data into scope.

## Dependency resolution

Orchard builds a directed acyclic graph of all actions and components in the
scenario, topologically sorts it, and executes nodes in order. Independent
branches run sequentially today; parallel execution is a future addition.

Cycles are detected at plan time and reported as errors — you cannot have two
actions that depend on each other.

## Output capture

Each provider decides what the outputs of its action look like. For example:

- The `sql` action returns the first row of the result set as an object, keyed
  by column name.
- The `request` action returns an object with `status`, `body`, and `headers`.
- The `run` action returns an object with `stdout`, `stderr`, and `exit_code`.

Downstream expressions access these as ordinary HCL attribute traversals:

```hcl
action "sql" "next" {
  query = "SELECT * FROM users WHERE id = ${action.sql.previous.user_id}"
}
```

See the [provider reference](../../providers/) for each provider's exact output
shape.

## Advanced action attributes

Actions support a few attributes that are handled by the engine rather than passed
to the provider:

- **`provider = "name"`** — explicitly route the action to a named provider.
- **`depends_on = [ ... ]`** — add explicit dependency edges.
- **`timeout = "30s"`** — wrap the provider's execute call in a Go-duration
  timeout. Exceeding the timeout fails the action.
- **`retry { }`** — retry the action on failure with backoff. See
  [HCL schema reference](../../reference/hcl-schema/#retry).

---
title: Calling gRPC services
weight: 70
---

**Goal.** Build state through your own gRPC services (an accounts service that owns
the signup rules, a billing service that has to create the customer itself)
without generated stubs, `grpcurl`, or JSON to decode.

`grpc_call` takes a request as an HCL object and returns the response as a typed
object. Orchard learns the service's shape from the server or from a descriptor
set, checks every call against it before anything runs, and lets later actions
reference response fields directly.

```hcl
scenario "signup" {
  required_providers {
    accounts = { source = "builtin/grpc" }
    postgres = { source = "builtin/postgres" }
  }

  provider "accounts" {
    address = "localhost:50051"
  }

  action "grpc_call" "org" {
    method  = "accounts.v1.Accounts/CreateOrg"
    request = { name = "Acme", plan = "PLAN_TEAM", owner = { email = "admin@example.com" } }
  }

  action "postgres_insert" "invoice" {
    table  = "invoices"
    values = { org_id = action.grpc_call.org.response.org.id, amount = 100 }
  }
}
```

## Where the service shapes come from

**Server reflection** is the default. If your server registers the reflection
service (`reflection.Register(s)` in Go, `grpc_reflection` in Python, and so on),
there's nothing to configure. Orchard fetches the descriptors once per run.

**A descriptor set** works for servers without reflection, and pins the shapes to
what's in your repo. Build one from your protos and point the provider at it:

```sh
buf build -o protos/api.binpb
# or
protoc --include_imports --descriptor_set_out=protos/api.binpb -I protos protos/**/*.proto
```

```hcl
provider "accounts" {
  address        = "localhost:50051"
  descriptor_set = "./protos/api.binpb"
}
```

`--include_imports` matters with `protoc`: the set needs the files your protos
import, including Google's well-known types.

## Checking calls at plan time

Before a run starts, every call is checked against the live descriptors: the
service and method exist, the method is unary, every request field exists, values
have the right types, enum values are members, and at most one field of a `oneof`
is set. Mistakes come back with suggestions:

```text
action.grpc_call.org (scenarios/signup.hcl:14): accounts.v1.CreateOrgRequest has no field "nme" (did you mean "name"?)
```

To get the same checks from `orchard plan`, with no server running, capture the
descriptors once and commit the snapshot:

```sh
orchard schema pull scenarios/signup.hcl   # writes .orchard/schema/accounts.json
orchard plan scenarios/signup.hcl
```

With a snapshot, plan also knows each response's type, so a reference like
`action.grpc_call.org.response.org.idd` fails at plan. With `descriptor_set`
configured, plan uses that instead and needs no snapshot.

## Values

Fields use their proto names (`org_id`, not `orgId`). 64-bit integers are
numbers, `bytes` are base64 strings, enums are value names, and `Timestamp` is an
RFC 3339 string, so `timeadd(now(), "24h")` fits a `Timestamp` field
directly. The [provider reference](/providers/grpc/) has the full table.

## Cleaning up

gRPC has no general way to undo a call, so `grpc_call` has no automatic teardown.
Give calls that create something a teardown block that calls the matching delete:

```hcl
action "grpc_call" "org" {
  method  = "accounts.v1.Accounts/CreateOrg"
  request = { name = "Acme" }

  lifecycle {
    teardown {
      method  = "accounts.v1.Accounts/DeleteOrg"
      request = { org_id = self.response.org.id }
    }
  }
}
```

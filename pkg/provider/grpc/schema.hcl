provider "grpc" {
  version  = "1"
  protocol = "1"

  description = "Unary gRPC calls, with no generated code."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - Service shapes come from server reflection, or from `descriptor_set` for servers
      without it. Before the run starts, every call is checked against them: the
      service and method exist, the method is unary, and every request field exists
      and has the right type.
    - `orchard schema pull` captures the descriptors as a snapshot, so `orchard plan`
      checks calls, and references to response fields, with no server running.
    - Field names are the proto names (`org_id`, not `orgId`). 64-bit integers are
      numbers, not strings. `bytes` are base64 strings, enums are value names,
      `Timestamp` is an RFC 3339 string, `Duration` a string like `"1.5s"`, and
      `FieldMask` a comma-separated string. `Struct`, `Value`, `ListValue` and `Any`
      take any value. Wrapper types are their wrapped value, or null.
    - In responses, unset scalars are their zero values; unset messages, `optional`
      fields and `oneof` members are null. So a response has the same shape on every
      call.
    - A call that returns an error status fails the action with the code, message
      and any details.
    - The action-level `timeout` is the call's deadline.
    - Only unary methods are supported.
    - Calls have no automatic teardown; give them a `lifecycle { teardown { } }` block
      that calls the matching delete method.
  MD

  config {
    attribute "address" {
      type        = string
      required    = true
      description = "Server address, `host:port`."
    }
    attribute "tls" {
      type        = bool
      default     = false
      description = "Use TLS. Plaintext is the default, since local servers usually are."
    }
    attribute "ca_file" {
      type        = string
      path        = true
      description = "CA bundle (PEM) for TLS, instead of the system roots. Relative to the scenario file."
    }
    attribute "server_name" {
      type        = string
      description = "TLS server name, if it differs from the host in `address`."
    }
    attribute "descriptor_set" {
      type        = string
      path        = true
      description = <<-MD
        A serialized `FileDescriptorSet`, used instead of server reflection, at plan and
        at run. Relative to the scenario file. Build one with
        `buf build -o api.binpb` or `protoc --include_imports --descriptor_set_out=api.binpb`.
      MD
    }
    attribute "default_metadata" {
      type        = map(string)
      description = "Metadata sent with every call. A call's `metadata` overrides it key by key."
    }
  }

  action "grpc_call" {
    description = "Call a unary method."

    attribute "method" {
      type        = string
      required    = true
      description = "`package.Service/Method`. grpcurl's `package.Service.Method` works too."
    }
    attribute "request" {
      type        = any
      default     = {}
      description = "The request message, as an object of `field = value`. Omitted fields are unset."
    }
    attribute "metadata" {
      type        = map(string)
      description = "Metadata (headers) for this call."
    }

    output {
      type = object({
        response = any,
        headers  = map(string),
        trailers = map(string),
      })
      description = <<-MD
        | Field | Type | Description |
        |-------|------|-------------|
        | `response` | object | The response message. Its type comes from the method's descriptor. |
        | `headers` | `map(string)` | Response headers, first value per key. Binary (`-bin`) values are base64. |
        | `trailers` | `map(string)` | Response trailers, likewise. |
      MD
    }

    example {
      description = "Create an org, then use its id in a second call and a Postgres insert."
      code        = <<-HCL
        action "grpc_call" "org" {
          method  = "accounts.v1.Accounts/CreateOrg"
          request = { name = var.org, plan = "PLAN_TEAM", owner = { email = "admin@example.com" } }
        }

        action "grpc_call" "suspend" {
          method  = "accounts.v1.Accounts/SuspendOrg"
          request = { org_id = action.grpc_call.org.response.org.id }
        }

        action "postgres_insert" "invoice" {
          table  = "invoices"
          values = { org_id = action.grpc_call.org.response.org.id, amount = 100 }
        }
      HCL
    }
  }
}

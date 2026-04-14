package protocol

import "encoding/json"

// DescribeParams is the payload of a describe request. Empty today;
// the named type reserves room for future additions (capability
// negotiation, requested-schema filters) without bumping the
// protocol version.
type DescribeParams struct{}

// DescribeResult returns the provider's full schema. Callers use it
// to validate action bodies and type-check output references.
type DescribeResult struct {
	Schema ProviderSchema `json:"schema"`
}

// ConfigureParams carries the evaluated `provider "name" {}` block
// from the scenario, encoded as JSON-serialized cty values keyed by
// attribute name. Decoding back to cty values is the caller's
// responsibility using the type information from
// ProviderSchema.Config.
type ConfigureParams struct {
	Config map[string]json.RawMessage `json:"config,omitempty"`
}

// ConfigureResult is the successful response to a configure call.
// Empty today; kept as a named type so providers can grow the
// response shape (observability hints, capability advertisements)
// without breaking the protocol.
type ConfigureResult struct{}

// ExecuteParams is the payload of an execute request.
//
// Attrs values are JSON-encoded cty values. The engine has already
// evaluated the HCL action body against the scenario's eval context,
// so attribute values are fully resolved.
type ExecuteParams struct {
	Action string                     `json:"action"`
	Attrs  map[string]json.RawMessage `json:"attrs"`
}

// ExecuteResult is the payload of an execute response.
//
// Outputs values are JSON-encoded cty values. They are made available
// to downstream nodes as `action.<provider>.<name>.<output>` references.
type ExecuteResult struct {
	Outputs map[string]json.RawMessage `json:"outputs"`
}

// ShutdownParams is the payload of a shutdown request. Empty today
// but named so fields (drain timeouts, final flush flags) can be
// added without a protocol version bump.
type ShutdownParams struct{}

// ShutdownResult is the payload of a shutdown response.
type ShutdownResult struct{}

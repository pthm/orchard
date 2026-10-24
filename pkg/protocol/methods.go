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
//
// Undo is set when Orchard will record an undo for this action: it has
// no `lifecycle { teardown { } }` block and doesn't opt out with
// `managed_teardown = false`. A provider that knows how to reverse the
// action returns ExecuteResult.Undo; one that doesn't ignores it.
type ExecuteParams struct {
	Action string                     `json:"action"`
	Attrs  map[string]json.RawMessage `json:"attrs"`
	Undo   *UndoOptions               `json:"undo,omitempty"`
}

// UndoOptions carries the scenario's say in automatic teardown.
type UndoOptions struct {
	// Requested is true when the action sets
	// `lifecycle { managed_teardown = true }`: return an undo even where
	// the provider wouldn't by default.
	Requested bool `json:"requested,omitempty"`
}

// ExecuteResult is the payload of an execute response.
//
// Outputs values are JSON-encoded cty values. They are made available
// to downstream nodes as `action.<provider>.<name>.<output>` references.
//
// Undo, returned only when the request carried ExecuteParams.Undo, is
// the call that reverses this action. Orchard stores it in the run
// record and `orchard teardown` executes it.
type ExecuteResult struct {
	Outputs map[string]json.RawMessage `json:"outputs"`
	Undo    *Invocation                `json:"undo,omitempty"`
}

// Invocation is an action call to make later: one of the provider's own
// action types and its attributes, encoded as in ExecuteParams.
type Invocation struct {
	Action string                     `json:"action"`
	Attrs  map[string]json.RawMessage `json:"attrs"`
}

// ShutdownParams is the payload of a shutdown request. Empty today
// but named so fields (drain timeouts, final flush flags) can be
// added without a protocol version bump.
type ShutdownParams struct{}

// ShutdownResult is the payload of a shutdown response.
type ShutdownResult struct{}

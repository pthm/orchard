package protocol

import "encoding/json"

// InitializeParams is the payload of the initialize request.
//
// Config carries the evaluated `provider "name" {}` block from the
// scenario, encoded as JSON-serialized cty values keyed by attribute
// name. Decoding back to cty values is the caller's responsibility,
// using the type information from ProviderSchema.Config.
type InitializeParams struct {
	Config map[string]json.RawMessage `json:"config,omitempty"`
}

// InitializeResult is the payload of the initialize response.
//
// A provider returns its full ProviderSchema on initialize so the
// engine can validate action bodies at plan time and type-check
// output references in scenario expressions.
type InitializeResult struct {
	Schema ProviderSchema `json:"schema"`
}

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

// ShutdownParams is the payload of the shutdown request. It is empty
// today but exists as a named type so future fields (drain timeouts,
// final flush flags) can be added without a protocol version bump.
type ShutdownParams struct{}

// ShutdownResult is the payload of the shutdown response.
type ShutdownResult struct{}

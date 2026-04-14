// Package protocol defines the wire format and schema model shared between
// Orchard and its providers.
//
// Providers communicate with Orchard over JSON Lines on stdio: one JSON
// object per line, with request/response matched by id. The same types
// that describe the wire format also describe in-process providers, so
// builtin and external providers share a single schema model.
//
// The on-disk authoring format is HCL (see ParseHCLSchema); the on-wire
// format is JSON. Both round-trip through the same ProviderSchema struct.
package protocol

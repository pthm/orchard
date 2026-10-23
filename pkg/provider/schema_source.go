package provider

import (
	"context"
	"encoding/json"
)

// SchemaSource is implemented by providers that can capture the shape
// of the system they act on (a database's tables, later an API spec),
// for `orchard schema pull` to store as a snapshot. It's optional;
// providers that don't implement it are skipped by `schema pull`.
//
// PullSchema is called after Configure. It must only read metadata,
// never data, and the capture must not contain connection details or
// secrets, since snapshots are committed.
type SchemaSource interface {
	PullSchema(ctx context.Context, opts PullOptions) (*Capture, error)
}

// PullOptions narrows what a SchemaSource captures.
type PullOptions struct {
	// Schemas limits database providers to the named schemas
	// (namespaces). Empty means all. Providers without namespaces
	// ignore it.
	Schemas []string
}

// Capture is what a SchemaSource returns.
type Capture struct {
	// Kind names the body's format, e.g. "database" (pkg/dbschema).
	Kind string
	// Target identifies what was captured, without secrets, e.g.
	// "postgres/app_dev". `schema pull` uses it to avoid overwriting
	// one database's snapshot with another's.
	Target string
	// Body is the kind-specific JSON body.
	Body json.RawMessage
}

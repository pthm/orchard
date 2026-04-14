package external

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// schemaProbeTimeout bounds how long LoadSchema waits for the
// binary to write its schema and exit. The schema subcommand is
// meant to be a cheap, synchronous lookup — if it hangs, the user
// is likely running an old binary that doesn't implement schema.
const schemaProbeTimeout = 10 * time.Second

// LoadSchema asks an external provider binary to describe itself
// without starting a long-lived process. It runs `<path> schema`,
// reads the JSON schema document the binary writes to stdout, and
// returns a Provider that responds to Schema() but errors on
// Configure / Execute.
//
// This is the entry point used by `orchard plan`. Plan is a
// read-only pass — it type-checks scenario references against
// provider schemas and does not run any actions. Using Spawn in
// plan would launch the full subprocess, run the describe
// handshake, and leave a subprocess alive until Close, which
// violates the plan contract (no side effects) and makes plan
// unnecessarily expensive for a pure type check.
//
// The binary must treat "schema" as a first-class subcommand:
// write the ProviderSchema as JSON on stdout, then exit with
// status 0. Any stderr output is surfaced in the error message
// when LoadSchema fails, so authors can see what went wrong.
func LoadSchema(path string) (*SchemaProvider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), schemaProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "schema")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("external schema %s: %w (stderr: %s)",
				path, err, bytes.TrimSpace(stderr.Bytes()))
		}
		return nil, fmt.Errorf("external schema %s: %w", path, err)
	}

	var schema protocol.ProviderSchema
	if err := json.Unmarshal(stdout.Bytes(), &schema); err != nil {
		return nil, fmt.Errorf("external schema %s: decode: %w", path, err)
	}
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("external schema %s: invalid: %w", path, err)
	}
	return &SchemaProvider{path: path, schema: schema}, nil
}

// SchemaProvider satisfies provider.Provider with schema-only
// behaviour. It is the returned type of LoadSchema and exists so
// plan-mode registries can validate scenarios against provider
// schemas without being able to execute anything.
//
// Configure and Execute deliberately error — if either runs during
// plan, something has reached into a provider that should not have
// been addressable in this mode, and the loud failure is safer
// than a silent no-op.
type SchemaProvider struct {
	path   string
	schema protocol.ProviderSchema
}

var _ provider.Provider = (*SchemaProvider)(nil)

// Schema returns the schema decoded during LoadSchema.
func (p *SchemaProvider) Schema() protocol.ProviderSchema { return p.schema }

// Configure refuses to run in plan mode.
func (p *SchemaProvider) Configure(context.Context, map[string]cty.Value) error {
	return fmt.Errorf("external %s: plan-mode schema provider cannot be configured", p.path)
}

// Execute refuses to run in plan mode.
func (p *SchemaProvider) Execute(context.Context, string, map[string]cty.Value) (map[string]cty.Value, error) {
	return nil, fmt.Errorf("external %s: plan-mode schema provider cannot execute", p.path)
}

// Close is a no-op. LoadSchema already waited for the probe
// subprocess to exit, so there is nothing to tear down.
func (p *SchemaProvider) Close() error { return nil }

package external

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

// shutdownTimeout bounds how long Close waits for a subprocess to
// exit voluntarily after receiving the shutdown RPC. Past this,
// Close force-kills the process so a misbehaving provider can't
// block orchard's own exit indefinitely.
const shutdownTimeout = 5 * time.Second

// Provider is an Orchard provider that lives in a separate process
// and communicates over JSON Lines on stdio. It satisfies the same
// provider.Provider interface as builtins, so the engine does not
// distinguish between the two — one code path covers both kinds.
//
// Lifecycle: Spawn starts the subprocess, completes the describe
// handshake, and caches the schema. Configure, Execute, and Close
// then behave like any in-process provider.
type Provider struct {
	cmd    *exec.Cmd
	client *Client
	stderr io.ReadCloser
	schema protocol.ProviderSchema
	logger *log.Logger
	done   chan error // receives cmd.Wait() result exactly once
}

// Compile-time assertion that Provider satisfies the interface.
var _ provider.Provider = (*Provider)(nil)

// Spawn launches the provider binary at path and performs the
// describe handshake. The returned Provider is ready to Configure
// and Execute against. If the subprocess fails to start, responds
// with an unparseable frame, or returns an invalid schema, Spawn
// kills the process and returns an error.
//
// Stderr from the subprocess is streamed to the default logger
// prefixed with the provider's name. Providers are expected to use
// stderr for diagnostics and reserve stdout exclusively for
// JSON Lines protocol traffic.
func Spawn(path string, args ...string) (*Provider, error) {
	cmd := exec.Command(path, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("external: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("external: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("external: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("external: start %s: %w", path, err)
	}

	p := &Provider{
		cmd:    cmd,
		client: NewClient(stdout, stdin),
		stderr: stderr,
		logger: log.Default(),
		done:   make(chan error, 1),
	}

	// Reap the subprocess in the background so Close can wait on a
	// channel rather than racing against Wait being called twice.
	go func() { p.done <- cmd.Wait() }()

	// Start stderr drain before the handshake: a misconfigured
	// provider's first act is often to print an error to stderr
	// and exit, and we want that visible rather than swallowed.
	go p.drainStderr("unknown")

	var result protocol.DescribeResult
	if err := p.client.Roundtrip(protocol.MethodDescribe, protocol.DescribeParams{}, &result); err != nil {
		p.forceClose()
		return nil, fmt.Errorf("external: describe %s: %w", path, err)
	}
	if err := result.Schema.Validate(); err != nil {
		p.forceClose()
		return nil, fmt.Errorf("external: invalid schema from %s: %w", path, err)
	}
	p.schema = result.Schema
	return p, nil
}

// drainStderr copies subprocess stderr to the logger, line by line.
// Runs until stderr closes (the subprocess exits or closes its
// stream). The providerName argument lets the log prefix use the
// real schema name once Spawn has parsed it.
func (p *Provider) drainStderr(providerName string) {
	scanner := bufio.NewScanner(p.stderr)
	for scanner.Scan() {
		name := providerName
		if p.schema.Name != "" {
			name = p.schema.Name
		}
		p.logger.Printf("[%s] %s", name, scanner.Text())
	}
}

// Schema returns the schema returned by the subprocess during the
// describe handshake.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

// Configure marshals the config map to JSON using the provider's
// declared config types and sends it to the subprocess.
func (p *Provider) Configure(ctx context.Context, config map[string]cty.Value) error {
	raw, err := encodeAttrs(config, p.schema.Config)
	if err != nil {
		return fmt.Errorf("external: configure %s: %w", p.schema.Name, err)
	}
	if err := p.client.Roundtrip(protocol.MethodConfigure, protocol.ConfigureParams{Config: raw}, nil); err != nil {
		return fmt.Errorf("external: configure %s: %w", p.schema.Name, err)
	}
	return nil
}

// Execute marshals attrs, invokes the subprocess, and decodes the
// returned outputs as dynamically-typed cty values. Output typing
// from the action schema is not yet used for decoding — matching
// how in-process providers return dynamic output maps — and will
// come in the plan-time reference-typing milestone.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	actionSchema, ok := p.schema.Actions[action]
	if !ok {
		return nil, fmt.Errorf("external %s: unknown action %q", p.schema.Name, action)
	}
	raw, err := encodeAttrs(attrs, actionSchema.Attrs)
	if err != nil {
		return nil, fmt.Errorf("external %s.%s: %w", p.schema.Name, action, err)
	}

	var result protocol.ExecuteResult
	err = p.client.Roundtrip(protocol.MethodExecute, protocol.ExecuteParams{
		Action: action, Attrs: raw,
	}, &result)
	if err != nil {
		return nil, fmt.Errorf("external %s.%s: %w", p.schema.Name, action, err)
	}

	outputs, err := protocol.UnmarshalAttrs(result.Outputs, nil)
	if err != nil {
		return nil, fmt.Errorf("external %s.%s: decode outputs: %w", p.schema.Name, action, err)
	}
	return outputs, nil
}

// Close sends the shutdown RPC and waits for the subprocess to
// exit. A non-responsive provider is force-killed after the
// shutdown timeout.
func (p *Provider) Close() error {
	// Best-effort shutdown; the subprocess may already be dead.
	_ = p.client.Roundtrip(protocol.MethodShutdown, protocol.ShutdownParams{}, nil)

	select {
	case err := <-p.done:
		return waitErr(err)
	case <-time.After(shutdownTimeout):
		// Escalate: force-kill and wait for the reaper goroutine.
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		return fmt.Errorf("external %s: did not shut down within %s; killed", p.schema.Name, shutdownTimeout)
	}
}

// forceClose kills the subprocess without attempting a graceful
// shutdown. Used during Spawn error paths where there's no
// guarantee the process even understands the protocol.
func (p *Provider) forceClose() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	// Drain the reaper so we don't leak the goroutine even on
	// an error exit from Spawn.
	<-p.done
}

// waitErr filters the exit-status error returned by cmd.Wait so
// that a normal exit (code 0) looks like nil to callers. A
// subprocess that exits non-zero after shutdown is a failure worth
// reporting even though shutdown succeeded.
func waitErr(err error) error {
	if err == nil {
		return nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 0 {
		return nil
	}
	return err
}

// encodeAttrs marshals cty values for the wire, using declared
// schema types where available for canonical encoding and the
// value's own type as a fallback for attributes the schema does
// not declare (which means DynamicPseudoType-typed pass-through).
func encodeAttrs(attrs map[string]cty.Value, schemaAttrs map[string]protocol.AttrSchema) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(attrs))
	for name, val := range attrs {
		t := val.Type()
		if decl, ok := schemaAttrs[name]; ok {
			parsed, err := decl.CtyType()
			if err != nil {
				return nil, fmt.Errorf("schema type for %q: %w", name, err)
			}
			if parsed != cty.DynamicPseudoType {
				t = parsed
			}
		}
		raw, err := protocol.MarshalValueAs(val, t)
		if err != nil {
			return nil, fmt.Errorf("attr %q: %w", name, err)
		}
		out[name] = raw
	}
	return out, nil
}

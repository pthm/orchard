package external

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
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
//
// Concurrency: the stderr-drain goroutine runs alongside the
// handshake, so it reads p.schema while Spawn is about to write
// it. Every access to schema goes through schemaMu; drainStderr,
// Schema, and any other reader stay read-locked; Spawn and Close
// take the write lock briefly when mutating it.
type Provider struct {
	cmd    *exec.Cmd
	client *Client
	stderr io.ReadCloser
	path   string
	logger *log.Logger
	done   chan error // receives cmd.Wait() result exactly once

	schemaMu sync.RWMutex
	schema   protocol.ProviderSchema
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
		path:   path,
		logger: log.Default(),
		done:   make(chan error, 1),
	}

	// Reap the subprocess in the background so Close can wait on a
	// channel rather than racing against Wait being called twice.
	go func() { p.done <- cmd.Wait() }()

	// Start stderr drain before the handshake: a misconfigured
	// provider's first act is often to print an error to stderr
	// and exit, and we want that visible rather than swallowed.
	// The drain reads schema under a read-lock, so the log prefix
	// flips from the path to the schema name the moment describe
	// completes and setSchema returns.
	go p.drainStderr()

	// Bound the describe handshake: a subprocess that starts but
	// never writes a response must not wedge Spawn indefinitely.
	handshakeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var result protocol.DescribeResult
	if err := p.client.Roundtrip(handshakeCtx, protocol.MethodDescribe, protocol.DescribeParams{}, &result); err != nil {
		p.forceClose()
		return nil, fmt.Errorf("external: describe %s: %w", path, err)
	}
	if err := result.Schema.Validate(); err != nil {
		p.forceClose()
		return nil, fmt.Errorf("external: invalid schema from %s: %w", path, err)
	}
	p.setSchema(result.Schema)
	return p, nil
}

// setSchema writes p.schema under the mutex. Exactly one call
// per successful Spawn; later reads see the populated schema.
func (p *Provider) setSchema(s protocol.ProviderSchema) {
	p.schemaMu.Lock()
	p.schema = s
	p.schemaMu.Unlock()
}

// getSchema returns the current schema under the read lock.
// Safe to call concurrently with setSchema.
func (p *Provider) getSchema() protocol.ProviderSchema {
	p.schemaMu.RLock()
	defer p.schemaMu.RUnlock()
	return p.schema
}

// logPrefix returns the best available identifier for stderr log
// lines: the schema name once it's been set, otherwise the path
// orchard launched. Both are stable strings once read.
func (p *Provider) logPrefix() string {
	if name := p.getSchema().Name; name != "" {
		return name
	}
	return p.path
}

// drainStderr copies subprocess stderr to the logger, line by line.
// Runs until stderr closes (the subprocess exits or closes its
// stream). Each line's prefix is resolved under the schema lock,
// so the prefix transitions cleanly from the binary path to the
// provider's schema name once describe completes.
func (p *Provider) drainStderr() {
	scanner := bufio.NewScanner(p.stderr)
	for scanner.Scan() {
		p.logger.Printf("[%s] %s", p.logPrefix(), scanner.Text())
	}
}

// Schema returns the schema returned by the subprocess during the
// describe handshake.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.getSchema()
}

// Configure marshals the config map to JSON using the provider's
// declared config types and sends it to the subprocess.
func (p *Provider) Configure(ctx context.Context, config map[string]cty.Value) error {
	schema := p.getSchema()
	raw, err := encodeAttrs(config, schema.Config)
	if err != nil {
		return fmt.Errorf("external: configure %s: %w", schema.Name, err)
	}
	if err := p.client.Roundtrip(ctx, protocol.MethodConfigure, protocol.ConfigureParams{Config: raw}, nil); err != nil {
		return fmt.Errorf("external: configure %s: %w", schema.Name, err)
	}
	return nil
}

// Execute marshals attrs, invokes the subprocess, and decodes the
// returned outputs as dynamically-typed cty values. Output typing
// from the action schema is not yet used for decoding — matching
// how in-process providers return dynamic output maps — and will
// come in the plan-time reference-typing milestone.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	out, _, err := p.execute(ctx, action, attrs, nil)
	return out, err
}

var _ provider.Undoer = (*Provider)(nil)

// ExecuteUndo runs the action like Execute and asks the subprocess for
// the call that undoes it. Providers that don't describe undos return
// none, and the action gets no automatic teardown.
func (p *Provider) ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, opts provider.InverseOptions) (map[string]cty.Value, *provider.Invocation, error) {
	return p.execute(ctx, action, attrs, &protocol.UndoOptions{Requested: opts.Requested})
}

func (p *Provider) execute(ctx context.Context, action string, attrs map[string]cty.Value, undoOpts *protocol.UndoOptions) (map[string]cty.Value, *provider.Invocation, error) {
	schema := p.getSchema()
	actionSchema, ok := schema.Actions[action]
	if !ok {
		return nil, nil, fmt.Errorf("external %s: unknown action %q", schema.Name, action)
	}
	raw, err := encodeAttrs(attrs, actionSchema.Attrs)
	if err != nil {
		return nil, nil, fmt.Errorf("external %s.%s: %w", schema.Name, action, err)
	}

	var result protocol.ExecuteResult
	err = p.client.Roundtrip(ctx, protocol.MethodExecute, protocol.ExecuteParams{
		Action: action, Attrs: raw, Undo: undoOpts,
	}, &result)
	if err != nil {
		return nil, nil, fmt.Errorf("external %s.%s: %w", schema.Name, action, err)
	}

	outputs, err := protocol.UnmarshalAttrs(result.Outputs, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("external %s.%s: decode outputs: %w", schema.Name, action, err)
	}
	if result.Undo == nil {
		return outputs, nil, nil
	}
	// Check the undo now, while the action's failure can still say
	// why, rather than when teardown runs it.
	undoSchema, ok := schema.Actions[result.Undo.Action]
	if !ok {
		return nil, nil, fmt.Errorf("external %s.%s: undo names unknown action %q", schema.Name, action, result.Undo.Action)
	}
	types := make(map[string]cty.Type, len(undoSchema.Attrs))
	for name, a := range undoSchema.Attrs {
		if types[name], err = a.CtyType(); err != nil {
			return nil, nil, fmt.Errorf("external %s.%s: undo: %w", schema.Name, action, err)
		}
	}
	for name := range result.Undo.Attrs {
		if _, ok := types[name]; !ok {
			return nil, nil, fmt.Errorf("external %s.%s: undo: %s has no attribute %q", schema.Name, action, result.Undo.Action, name)
		}
	}
	undoAttrs, err := protocol.UnmarshalAttrs(result.Undo.Attrs, types)
	if err != nil {
		return nil, nil, fmt.Errorf("external %s.%s: decode undo: %w", schema.Name, action, err)
	}
	return outputs, &provider.Invocation{Action: result.Undo.Action, Attrs: undoAttrs}, nil
}

// Close attempts a graceful shutdown of the subprocess and force-
// kills it if the total shutdown window elapses without a clean
// exit. The shutdown RPC and the subsequent process wait share a
// single deadline — total close time is bounded by shutdownTimeout
// regardless of whether the provider hangs on the RPC, hangs on
// exit, or hangs on both.
func (p *Provider) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Best-effort shutdown. The RPC either returns promptly, fails
	// fast (poisoned client from a prior ctx cancel), or is abandoned
	// when ctx deadline hits — in any case we proceed to the wait.
	_ = p.client.Roundtrip(ctx, protocol.MethodShutdown, protocol.ShutdownParams{}, nil)

	select {
	case err := <-p.done:
		return waitErr(err)
	case <-ctx.Done():
		// Either the RPC consumed the deadline, or the process
		// refused to exit after a clean shutdown. Force-kill and
		// wait for the reaper goroutine.
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		return fmt.Errorf("external %s: did not shut down within %s; killed", p.getSchema().Name, shutdownTimeout)
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
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 0 {
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

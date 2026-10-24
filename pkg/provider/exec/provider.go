// Package exec is the builtin Orchard provider for shell command
// execution. One action — "run" — runs a program with optional
// args/env/stdin/cwd and captures stdout, stderr, and exit code.
//
// Non-zero exit is an error by default. That matches the most
// common use case (run a migration script, then do X) without
// introducing a scenario-level notion of conditional branching.
// Scenarios that want to tolerate failures will need the future
// retry block or an explicit "expected exit codes" attribute —
// neither is wired yet.
package exec

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

//go:embed schema.hcl
var schemaHCL []byte

// stderrPreview caps how much stderr we include in the error
// message when a command fails. Anything longer is truncated with
// a marker, since an unbounded stderr line makes orchard's own
// output unreadable.
const stderrPreview = 400

// Provider is the cmd provider implementation.
type Provider struct {
	schema     protocol.ProviderSchema
	defaultDir string
	defaultEnv map[string]string
}

// New returns an unconfigured cmd provider. Schema is parsed once
// up front so any error in the embedded schema.hcl fails loudly
// at binary startup rather than at first use.
func New() *Provider {
	return &Provider{schema: protocol.MustParseHCL(schemaHCL)}
}

var _ provider.Provider = (*Provider)(nil)

// Schema returns the embedded provider schema.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

// Configure captures provider-level defaults (cwd, env) that
// every action inherits unless it overrides.
func (p *Provider) Configure(_ context.Context, config map[string]cty.Value) error {
	if v, ok := config["dir"]; ok && !v.IsNull() {
		if v.Type() != cty.String {
			return fmt.Errorf("exec: dir must be a string, got %s", v.Type().FriendlyName())
		}
		p.defaultDir = v.AsString()
	}
	if v, ok := config["env"]; ok && !v.IsNull() {
		if !v.Type().IsMapType() && !v.Type().IsObjectType() {
			return fmt.Errorf("exec: env must be a map(string), got %s", v.Type().FriendlyName())
		}
		p.defaultEnv = ctyMapToStringMap(v)
	}
	return nil
}

// Execute dispatches to run.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if action == "exec_run" {
		return p.runCommand(ctx, attrs)
	}
	return nil, fmt.Errorf("exec: unknown action %q", action)
}

// Close has nothing to release.
func (p *Provider) Close() error { return nil }

// runCommand runs the described program and captures its output.
func (p *Provider) runCommand(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	command := attrs["command"].AsString()
	if command == "" {
		return nil, fmt.Errorf("exec.run: command is required")
	}

	var args []string
	if v, ok := attrs["args"]; ok && !v.IsNull() {
		it := v.ElementIterator()
		for it.Next() {
			_, val := it.Element()
			args = append(args, val.AsString())
		}
	}

	cmd := osexec.CommandContext(ctx, command, args...)
	killGroupOnCancel(cmd)

	// Working directory: per-action override wins over provider default.
	dir := p.defaultDir
	if v, ok := attrs["dir"]; ok && !v.IsNull() {
		dir = v.AsString()
	}
	if dir != "" {
		cmd.Dir = dir
	}

	// Environment: start from the host env, layer provider defaults,
	// then layer per-action env. Duplicates later in cmd.Env win,
	// which matches Go's documented behaviour.
	cmd.Env = os.Environ()
	for k, v := range p.defaultEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if v, ok := attrs["env"]; ok && !v.IsNull() {
		for k, vv := range ctyMapToStringMap(v) {
			cmd.Env = append(cmd.Env, k+"="+vv)
		}
	}

	if v, ok := attrs["stdin"]; ok && !v.IsNull() {
		cmd.Stdin = strings.NewReader(v.AsString())
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if ctxErr := ctx.Err(); runErr != nil && ctxErr != nil {
		return nil, fmt.Errorf("exec: run %s: %w", command, ctxErr)
	}

	exitCode := 0
	if runErr != nil {
		var exit *osexec.ExitError
		switch {
		case errors.As(runErr, &exit):
			exitCode = exit.ExitCode()
		default:
			// Start failed: executable missing, permission denied,
			// ctx cancelled. Surface the raw error rather than
			// pretending the command exited with a code.
			return nil, fmt.Errorf("exec: run %s: %w", command, runErr)
		}
	}

	if exitCode != 0 {
		return nil, fmt.Errorf("exec: run %s exited with code %d\n%s",
			quoteCommand(command, args),
			exitCode,
			formatStderrPreview(stderr.String()))
	}

	return map[string]cty.Value{
		"stdout":    cty.StringVal(stdout.String()),
		"stderr":    cty.StringVal(stderr.String()),
		"exit_code": cty.NumberIntVal(int64(exitCode)),
	}, nil
}

// quoteCommand renders a command+args for error messages. Naive
// quoting — we escape only spaces — since this is display-only,
// not shell-safe reconstruction.
func quoteCommand(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, maybeQuote(command))
	for _, a := range args {
		parts = append(parts, maybeQuote(a))
	}
	return strings.Join(parts, " ")
}

func maybeQuote(s string) string {
	if strings.ContainsAny(s, " \t\n\"") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// formatStderrPreview returns a short, multi-line excerpt of
// stderr for inclusion in an error message. Empty input returns
// the empty string; longer than stderrPreview bytes is truncated
// with a marker.
func formatStderrPreview(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return "(no stderr)"
	}
	if len(s) > stderrPreview {
		s = s[:stderrPreview] + "… (truncated)"
	}
	return "stderr:\n" + indent(s, "  ")
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// ctyMapToStringMap flattens a cty map/object value into a Go
// map[string]string. Non-string values are coerced best-effort —
// same shape as the http provider's helper, kept here as a local
// copy rather than a shared package since the semantics ("best-
// effort string coercion for env vars") are specific to this
// provider.
func ctyMapToStringMap(v cty.Value) map[string]string {
	out := make(map[string]string)
	if v.IsNull() {
		return out
	}
	it := v.ElementIterator()
	for it.Next() {
		k, val := it.Element()
		if val.IsNull() {
			out[k.AsString()] = ""
			continue
		}
		if val.Type() == cty.String {
			out[k.AsString()] = val.AsString()
			continue
		}
		out[k.AsString()] = val.GoString()
	}
	return out
}

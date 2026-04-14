package external

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestMain re-uses the test binary as its own subprocess fixture.
// When launched with GO_WANT_HELPER_PROCESS=1, the binary impersonates
// a minimal echo provider and speaks the wire protocol on stdio.
// Otherwise it runs the normal test suite.
//
// This pattern (borrowed from os/exec's own tests) lets us exercise
// the full Spawn → describe → configure → execute → shutdown loop
// without committing a separate fixture binary, and without building
// from a testdata directory that the go toolchain treats specially.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		runEchoHelper()
		return
	}
	os.Exit(m.Run())
}

// spawnHelper returns a Provider backed by this test binary running
// in helper mode. Tests that want happy-path subprocess behaviour
// use this; tests for error paths (bad binary, bad handshake) call
// Spawn directly with a crafted target.
func spawnHelper(t *testing.T) *Provider {
	t.Helper()
	os.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Cleanup(func() { os.Unsetenv("GO_WANT_HELPER_PROCESS") })
	p, err := Spawn(os.Args[0])
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return p
}

func TestSpawnAndSchema(t *testing.T) {
	p := spawnHelper(t)
	defer p.Close()

	s := p.Schema()
	if s.Name != "echo" {
		t.Errorf("schema name: got %q", s.Name)
	}
	if s.Version != "1" || s.Protocol != "1" {
		t.Errorf("version/protocol: %q/%q", s.Version, s.Protocol)
	}
	if _, ok := s.Actions["echo"]; !ok {
		t.Errorf("missing echo action")
	}
}

func TestConfigureThenExecute(t *testing.T) {
	p := spawnHelper(t)
	defer p.Close()

	if err := p.Configure(context.Background(), nil); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	outputs, err := p.Execute(context.Background(), "echo", map[string]cty.Value{
		"message": cty.StringVal("hello from test"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outputs["message"].AsString() != "hello from test" {
		t.Fatalf("echoed message: got %v", outputs["message"])
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	p := spawnHelper(t)
	defer p.Close()

	_, err := p.Execute(context.Background(), "nonexistent", map[string]cty.Value{})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestSpawnNonexistentBinary(t *testing.T) {
	_, err := Spawn("/nonexistent/binary/path")
	if err == nil {
		t.Fatal("expected spawn error")
	}
}

func TestCloseIsIdempotentViaGracefulExit(t *testing.T) {
	p := spawnHelper(t)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// runEchoHelper is the helper-process entry point. It reads JSON
// Lines requests from stdin, handles describe/configure/execute/
// shutdown, and writes responses to stdout. The shape mirrors the
// minimal example provider; this copy lives inside the test binary
// so there is no separate source file to ship.
func runEchoHelper() {
	r := bufio.NewReader(os.Stdin)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			continue
		}

		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "echo helper: bad request: %v\n", err)
			return
		}

		switch req.Method {
		case "describe":
			writeResp(req.ID, map[string]any{"schema": echoSchema()})
		case "configure":
			writeResp(req.ID, map[string]any{})
		case "execute":
			var params struct {
				Action string                     `json:"action"`
				Attrs  map[string]json.RawMessage `json:"attrs"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.Action != "echo" {
				writeErr(req.ID, "unknown_action", "unknown action: "+params.Action)
				continue
			}
			writeResp(req.ID, map[string]any{"outputs": params.Attrs})
		case "shutdown":
			writeResp(req.ID, map[string]any{})
			return
		default:
			writeErr(req.ID, "unknown_method", "unknown method: "+req.Method)
		}
	}
}

func echoSchema() map[string]any {
	return map[string]any{
		"name":     "echo",
		"version":  "1",
		"protocol": "1",
		"actions": map[string]any{
			"echo": map[string]any{
				"attrs": map[string]any{
					"message": map[string]any{"type": "string", "required": true},
				},
				"outputs": map[string]any{"type": "any"},
			},
		},
	}
}

func writeResp(id int64, result any) {
	raw, _ := json.Marshal(result)
	resp, _ := json.Marshal(map[string]any{"id": id, "result": json.RawMessage(raw)})
	fmt.Fprintf(os.Stdout, "%s\n", resp)
}

func writeErr(id int64, code, message string) {
	resp, _ := json.Marshal(map[string]any{
		"id":    id,
		"error": map[string]any{"code": code, "message": message},
	})
	fmt.Fprintf(os.Stdout, "%s\n", resp)
}

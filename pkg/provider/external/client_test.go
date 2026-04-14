package external

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/pthm/orchard/pkg/protocol"
)

// newPipeClient wires up a Client against an io.Pipe pair. The
// returned reader is the stream the "server" side of the pipe sees
// for incoming requests, and the returned writer is what it uses
// to push responses back to the Client.
func newPipeClient(t *testing.T) (*Client, *bufio.Reader, io.WriteCloser, func()) {
	t.Helper()
	// Direction 1: client → server (client writes, server reads).
	cToSReader, cToSWriter := io.Pipe()
	// Direction 2: server → client (server writes, client reads).
	sToCReader, sToCWriter := io.Pipe()

	c := NewClient(sToCReader, cToSWriter)
	cleanup := func() {
		cToSWriter.Close()
		sToCWriter.Close()
	}
	return c, bufio.NewReader(cToSReader), sToCWriter, cleanup
}

// handle is a tiny server loop that reads one request and writes
// one response built by f. It runs in a goroutine so the client's
// Roundtrip can complete. Returns any error encountered.
func handle(
	t *testing.T,
	serverIn *bufio.Reader,
	serverOut io.Writer,
	f func(req protocol.Request) protocol.Response,
) {
	t.Helper()
	var req protocol.Request
	if err := protocol.ReadMessage(serverIn, &req); err != nil {
		t.Errorf("server read: %v", err)
		return
	}
	resp := f(req)
	if err := protocol.WriteMessage(serverOut, resp); err != nil {
		t.Errorf("server write: %v", err)
	}
}

func TestClientRoundtripHappyPath(t *testing.T) {
	c, serverIn, serverOut, cleanup := newPipeClient(t)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handle(t, serverIn, serverOut, func(req protocol.Request) protocol.Response {
			if req.Method != "describe" {
				t.Errorf("method: got %q want describe", req.Method)
			}
			result, _ := json.Marshal(protocol.DescribeResult{
				Schema: protocol.ProviderSchema{Name: "x", Version: "1", Protocol: "1"},
			})
			return protocol.Response{ID: req.ID, Result: result}
		})
	}()

	var result protocol.DescribeResult
	if err := c.Roundtrip(protocol.MethodDescribe, protocol.DescribeParams{}, &result); err != nil {
		t.Fatalf("Roundtrip: %v", err)
	}
	if result.Schema.Name != "x" {
		t.Errorf("schema name: got %q", result.Schema.Name)
	}
	wg.Wait()
}

func TestClientRoundtripErrorResponse(t *testing.T) {
	c, serverIn, serverOut, cleanup := newPipeClient(t)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handle(t, serverIn, serverOut, func(req protocol.Request) protocol.Response {
			return protocol.Response{ID: req.ID, Error: &protocol.Error{
				Code: protocol.ErrCodeExecute, Message: "bad query",
			}}
		})
	}()

	err := c.Roundtrip(protocol.MethodExecute, protocol.ExecuteParams{Action: "x"}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	perr, ok := err.(*protocol.Error)
	if !ok {
		t.Fatalf("expected *protocol.Error, got %T: %v", err, err)
	}
	if perr.Code != protocol.ErrCodeExecute {
		t.Errorf("code: got %q", perr.Code)
	}
}

func TestClientRoundtripIDMismatch(t *testing.T) {
	c, serverIn, serverOut, cleanup := newPipeClient(t)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handle(t, serverIn, serverOut, func(req protocol.Request) protocol.Response {
			// Deliberately return a wrong ID. Real providers shouldn't
			// do this, but the client must catch it rather than silently
			// accepting misaligned responses.
			return protocol.Response{ID: req.ID + 1000, Result: json.RawMessage(`{}`)}
		})
	}()

	err := c.Roundtrip(protocol.MethodDescribe, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected id-mismatch error, got %v", err)
	}
}

func TestClientRoundtripNilResult(t *testing.T) {
	// A shutdown response has no meaningful result; callers pass
	// nil and the client must not try to decode into it.
	c, serverIn, serverOut, cleanup := newPipeClient(t)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handle(t, serverIn, serverOut, func(req protocol.Request) protocol.Response {
			return protocol.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
		})
	}()

	if err := c.Roundtrip(protocol.MethodShutdown, protocol.ShutdownParams{}, nil); err != nil {
		t.Fatalf("Roundtrip: %v", err)
	}
}

func TestClientIdsIncrement(t *testing.T) {
	// Two sequential Roundtrips should produce distinct ids, and
	// the client must match each response to the right request.
	c, serverIn, serverOut, cleanup := newPipeClient(t)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(2)
	var seen []int64
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			handle(t, serverIn, serverOut, func(req protocol.Request) protocol.Response {
				seen = append(seen, req.ID)
				return protocol.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
			})
		}()
	}

	if err := c.Roundtrip("a", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Roundtrip("b", nil, nil); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if len(seen) != 2 || seen[0] == seen[1] {
		t.Fatalf("expected two distinct ids, got %v", seen)
	}
}

// newBufferClient is a lighter-weight fixture than newPipeClient:
// the Client reads from a pre-populated bytes.Buffer, so tests can
// inject exact wire bytes without a server goroutine.
func newBufferClient(incoming string) (*Client, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return NewClient(strings.NewReader(incoming), out), out
}

func TestClientRoundtripTruncatedResponse(t *testing.T) {
	// Server died mid-response. ReadMessage surfaces
	// io.ErrUnexpectedEOF; Roundtrip wraps it.
	c, _ := newBufferClient(`{"id":1,"result":`)
	err := c.Roundtrip("x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "read response") {
		t.Fatalf("expected read-response error, got %v", err)
	}
}

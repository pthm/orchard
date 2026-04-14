package external

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

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
		// Close reader halves too — closing the writer unblocks
		// future reads, but pending writes need the reader to close
		// to unstick. Belt-and-braces, both directions.
		cToSWriter.Close()
		cToSReader.Close()
		sToCWriter.Close()
		sToCReader.Close()
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
	if err := c.Roundtrip(context.Background(), protocol.MethodDescribe, protocol.DescribeParams{}, &result); err != nil {
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

	err := c.Roundtrip(context.Background(), protocol.MethodExecute, protocol.ExecuteParams{Action: "x"}, nil)
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

	err := c.Roundtrip(context.Background(), protocol.MethodDescribe, nil, nil)
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

	if err := c.Roundtrip(context.Background(), protocol.MethodShutdown, protocol.ShutdownParams{}, nil); err != nil {
		t.Fatalf("Roundtrip: %v", err)
	}
}

func TestClientIdsIncrement(t *testing.T) {
	// Two sequential Roundtrips should produce distinct ids, and
	// the client must match each response to the right request.
	// A single server goroutine handles both requests in order —
	// Roundtrip is sequential, so there's no need for (and no safe
	// way to run) concurrent handlers sharing one pipe reader.
	c, serverIn, serverOut, cleanup := newPipeClient(t)
	defer cleanup()

	var wg sync.WaitGroup
	wg.Add(1)
	var seen []int64
	go func() {
		defer wg.Done()
		for i := 0; i < 2; i++ {
			handle(t, serverIn, serverOut, func(req protocol.Request) protocol.Response {
				seen = append(seen, req.ID)
				return protocol.Response{ID: req.ID, Result: json.RawMessage(`{}`)}
			})
		}
	}()

	if err := c.Roundtrip(context.Background(), "a", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Roundtrip(context.Background(), "b", nil, nil); err != nil {
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
	err := c.Roundtrip(context.Background(), "x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("expected truncated-stream error, got %v", err)
	}
}

func TestClientRoundtripContextCancellation(t *testing.T) {
	// Server never responds. Roundtrip must honour the ctx deadline
	// rather than blocking on the pipe forever.
	c, _, _, cleanup := newPipeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Roundtrip(ctx, "hang", nil, nil)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Roundtrip should return near the deadline, took %v", elapsed)
	}
}

func TestClientPoisonedAfterCancellation(t *testing.T) {
	// Once a call has been abandoned mid-read, the client must refuse
	// subsequent calls — a second reader against the same pipe would
	// race the still-in-flight goroutine.
	c, _, _, cleanup := newPipeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Roundtrip(ctx, "first", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first call: expected deadline exceeded, got %v", err)
	}

	err := c.Roundtrip(context.Background(), "second", nil, nil)
	if !errors.Is(err, ErrClientPoisoned) {
		t.Fatalf("second call: expected ErrClientPoisoned, got %v", err)
	}
}

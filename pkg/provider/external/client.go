// Package external implements a provider that runs as a subprocess
// and communicates with Orchard over JSON Lines on stdio. A pure
// byte-mover — it evaluates nothing itself, just marshals calls
// across the process boundary and returns whatever the subprocess
// produces. The engine already hands providers fully-evaluated
// attribute maps, so the adapter is the same shape regardless of
// whether the provider lives in-process (pkg/provider/postgres) or
// across a pipe (this package).
package external

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/pthm/orchard/pkg/protocol"
)

// Client is the low-level JSON-Lines transport. It takes a reader
// and writer — usually a subprocess's stdout and stdin, but pipes
// in tests — and exposes a single Roundtrip method for request/
// response pairs.
//
// Roundtrip holds a mutex for the duration of the exchange, making
// the client safe for concurrent callers but serial on the wire.
// That matches today's engine (which executes one action at a time
// per provider) and the JSON Lines framing (which has no way to
// interleave partial responses). When parallel execution lands,
// the upgrade is to a background reader goroutine dispatching to a
// pending-request map — a local change that does not affect
// callers.
type Client struct {
	mu   sync.Mutex
	r    *bufio.Reader
	w    io.Writer
	next atomic.Int64
}

// NewClient wraps r and w. The reader is buffered internally; the
// writer is used directly so callers can compose it with their own
// buffering if needed.
func NewClient(r io.Reader, w io.Writer) *Client {
	return &Client{r: bufio.NewReader(r), w: w}
}

// Roundtrip sends a request and decodes the matching response.
//
// params is any JSON-marshalable value. result must be a pointer
// into which the response's result field is decoded; pass nil if
// the method returns no meaningful payload. Errors returned by the
// provider are surfaced as *protocol.Error values so callers can
// inspect the code field if they need to distinguish error classes.
func (c *Client) Roundtrip(method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.next.Add(1)

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("external: marshal params for %q: %w", method, err)
		}
		paramsRaw = b
	}

	req := protocol.Request{ID: id, Method: method, Params: paramsRaw}
	if err := protocol.WriteMessage(c.w, req); err != nil {
		return fmt.Errorf("external: write %q: %w", method, err)
	}

	var resp protocol.Response
	if err := protocol.ReadMessage(c.r, &resp); err != nil {
		return fmt.Errorf("external: read response for %q: %w", method, err)
	}

	if resp.ID != id {
		return fmt.Errorf("external: response id %d does not match request id %d for %q",
			resp.ID, id, method)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("external: unmarshal result for %q: %w", method, err)
		}
	}
	return nil
}

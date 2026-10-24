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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/pthm/orchard/pkg/protocol"
)

// ErrClientPoisoned is returned when Roundtrip is called after a
// previous call was abandoned via context cancellation. Once a
// caller walks away from an in-flight read, the pipe's reader
// goroutine is still blocked and we cannot safely start another
// without racing it. Callers should tear the provider down and
// spawn a fresh one.
var ErrClientPoisoned = errors.New("external: client abandoned a prior call; cannot be reused")

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
	mu       sync.Mutex
	r        *bufio.Reader
	w        io.Writer
	next     atomic.Int64
	poisoned atomic.Bool
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
//
// When ctx is cancelled mid-exchange, Roundtrip returns ctx.Err
// and marks the client as poisoned. Subsequent calls fail fast
// with ErrClientPoisoned; the caller is expected to force-close
// the underlying transport (typically by killing the subprocess),
// which unblocks the still-in-flight reader goroutine and lets
// it exit cleanly. This avoids ever having two goroutines reading
// from the same pipe.
func (c *Client) Roundtrip(ctx context.Context, method string, params, result any) error {
	if c.poisoned.Load() {
		return ErrClientPoisoned
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.poisoned.Load() {
		return ErrClientPoisoned
	}
	// A call that gave up while queued behind another must not send:
	// nothing is on the wire for it yet, so the client stays usable.
	if err := ctx.Err(); err != nil {
		return err
	}

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

	type ioResult struct {
		resp protocol.Response
		err  error
	}
	done := make(chan ioResult, 1)
	go func() {
		// Write and read share a single goroutine so the outer select
		// below can abandon either half on ctx cancellation — pipe
		// writes can block indefinitely when the subprocess has
		// stopped reading, not just pipe reads.
		if err := protocol.WriteMessage(c.w, req); err != nil {
			done <- ioResult{err: fmt.Errorf("write %q: %w", method, err)}
			return
		}
		var resp protocol.Response
		err := protocol.ReadMessage(c.r, &resp)
		done <- ioResult{resp, err}
	}()

	select {
	case <-ctx.Done():
		c.poisoned.Store(true)
		return ctx.Err()
	case r := <-done:
		if r.err != nil {
			return fmt.Errorf("external: %q: %w", method, r.err)
		}
		if r.resp.ID != id {
			return fmt.Errorf("external: response id %d does not match request id %d for %q",
				r.resp.ID, id, method)
		}
		if r.resp.Error != nil {
			return r.resp.Error
		}
		if result != nil && len(r.resp.Result) > 0 {
			if err := json.Unmarshal(r.resp.Result, result); err != nil {
				return fmt.Errorf("external: unmarshal result for %q: %w", method, err)
			}
		}
		return nil
	}
}

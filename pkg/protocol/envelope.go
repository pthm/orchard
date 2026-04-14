package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Method names understood by the provider protocol.
const (
	MethodInitialize = "initialize"
	MethodExecute    = "execute"
	MethodShutdown   = "shutdown"
)

// Request is a single message from Orchard to a provider.
//
// Params is held as a raw JSON payload so that callers can unmarshal it
// into a method-specific struct (InitializeParams, ExecuteParams, ...)
// without the envelope needing to know about every method.
type Request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a single message from a provider back to Orchard.
// Exactly one of Result or Error is populated.
type Response struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a structured failure returned by a provider.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Well-known error codes. Providers are free to define additional codes,
// but these cover the conditions the engine reasons about directly.
const (
	ErrCodeInvalidRequest = "invalid_request"
	ErrCodeUnknownMethod  = "unknown_method"
	ErrCodeUnknownAction  = "unknown_action"
	ErrCodeValidation     = "validation_failed"
	ErrCodeConfigure      = "configure_failed"
	ErrCodeExecute        = "execute_failed"
	ErrCodeInternal       = "internal_error"
)

// WriteMessage encodes v as a single JSON Lines frame: compact JSON
// followed by a newline. The newline terminator is required — readers on
// the other end split on '\n'.
func WriteMessage(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("protocol: marshal message: %w", err)
	}
	// json.Encoder.Encode already appends '\n', so buf is a complete frame.
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("protocol: write message: %w", err)
	}
	return nil
}

// ReadMessage reads one '\n'-terminated JSON frame from r and decodes
// it into v. Returns io.EOF when the stream is closed cleanly between
// frames; a partial frame followed by EOF is reported as io.ErrUnexpectedEOF.
func ReadMessage(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		if err == io.EOF && len(line) == 0 {
			return io.EOF
		}
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return fmt.Errorf("protocol: read message: %w", err)
	}
	if err := json.Unmarshal(bytes.TrimRight(line, "\r\n"), v); err != nil {
		return fmt.Errorf("protocol: unmarshal message: %w", err)
	}
	return nil
}

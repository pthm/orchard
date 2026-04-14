package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestWriteReadMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer

	params, _ := json.Marshal(ExecuteParams{
		Action: "sql",
		Attrs:  map[string]json.RawMessage{"query": json.RawMessage(`"SELECT 1"`)},
	})
	req := Request{ID: 42, Method: MethodExecute, Params: params}
	if err := WriteMessage(&buf, req); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Fatalf("frame must end in newline, got %q", buf.String())
	}
	if bytes.Count(buf.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("frame must contain exactly one newline, got %q", buf.String())
	}

	var got Request
	if err := ReadMessage(bufio.NewReader(&buf), &got); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if got.ID != req.ID || got.Method != req.Method {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, req)
	}

	var p ExecuteParams
	if err := json.Unmarshal(got.Params, &p); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if p.Action != "sql" {
		t.Fatalf("action: got %q want %q", p.Action, "sql")
	}
}

func TestReadMessageEOF(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	var v Request
	if err := ReadMessage(r, &v); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestReadMessageUnexpectedEOF(t *testing.T) {
	// Partial frame with no trailing newline.
	r := bufio.NewReader(strings.NewReader(`{"id":1,"method":"foo"`))
	var v Request
	if err := ReadMessage(r, &v); err != io.ErrUnexpectedEOF {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadMessageMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	for _, id := range []int64{1, 2, 3} {
		if err := WriteMessage(&buf, Request{ID: id, Method: "ping"}); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
	}

	r := bufio.NewReader(&buf)
	for _, want := range []int64{1, 2, 3} {
		var got Request
		if err := ReadMessage(r, &got); err != nil {
			t.Fatalf("ReadMessage(id=%d): %v", want, err)
		}
		if got.ID != want {
			t.Fatalf("id: got %d want %d", got.ID, want)
		}
	}
	var extra Request
	if err := ReadMessage(r, &extra); err != io.EOF {
		t.Fatalf("expected io.EOF after last frame, got %v", err)
	}
}

func TestErrorFormatting(t *testing.T) {
	e := &Error{Code: ErrCodeExecute, Message: "bad query"}
	if got := e.Error(); got != "execute_failed: bad query" {
		t.Fatalf("short form: %q", got)
	}
	e.Detail = "column missing"
	if got := e.Error(); got != "execute_failed: bad query (column missing)" {
		t.Fatalf("detailed form: %q", got)
	}
}

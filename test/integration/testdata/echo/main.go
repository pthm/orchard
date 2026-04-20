// echo is a minimal external provider used as an end-to-end test
// fixture. TestMain compiles this into a temp binary and the
// integration scenarios launch it via exec: provider source.
//
// No imports from orchard — the test doubles as a live proof that
// the wire contract is language-independent.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *errorMessage   `json:"error,omitempty"`
}

type errorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {
	r := bufio.NewReader(os.Stdin)
	w := os.Stdout

	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			continue
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "echo: bad request: %v\n", err)
			return
		}

		switch req.Method {
		case "describe":
			writeResult(w, req.ID, map[string]any{"schema": schema()})
		case "configure":
			writeResult(w, req.ID, map[string]any{})
		case "execute":
			handleExecute(w, req)
		case "shutdown":
			writeResult(w, req.ID, map[string]any{})
			return
		default:
			writeError(w, req.ID, "unknown_method", "unknown method: "+req.Method)
		}
	}
}

func schema() map[string]any {
	return map[string]any{
		"name":     "echo",
		"version":  "1",
		"protocol": "1",
		"actions": map[string]any{
			"echo_say": map[string]any{
				"attrs": map[string]any{
					"message": map[string]any{"type": "string", "required": true},
				},
				"outputs": map[string]any{"type": "any"},
			},
			"echo_randomize": map[string]any{
				"attrs": map[string]any{
					"prefix": map[string]any{"type": "string", "required": true},
				},
				"outputs": map[string]any{"type": "any"},
			},
		},
	}
}

func handleExecute(w *os.File, req request) {
	var params struct {
		Action string                     `json:"action"`
		Attrs  map[string]json.RawMessage `json:"attrs"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(w, req.ID, "invalid_request", err.Error())
		return
	}
	switch params.Action {
	case "echo_say":
		writeResult(w, req.ID, map[string]any{"outputs": params.Attrs})
	case "echo_randomize":
		var prefix string
		if err := json.Unmarshal(params.Attrs["prefix"], &prefix); err != nil {
			writeError(w, req.ID, "invalid_request", "prefix: "+err.Error())
			return
		}
		value := fmt.Sprintf("%s-%08x", prefix, rng.Uint32())
		writeResult(w, req.ID, map[string]any{
			"outputs": map[string]any{"value": value},
		})
	default:
		writeError(w, req.ID, "unknown_action", "unknown action: "+params.Action)
	}
}

func writeResult(w *os.File, id int64, result any) {
	raw, _ := json.Marshal(result)
	resp := response{ID: id, Result: raw}
	b, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", b)
}

func writeError(w *os.File, id int64, code, message string) {
	resp := response{ID: id, Error: &errorMessage{Code: code, Message: message}}
	b, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", b)
}

---
title: Writing a provider
weight: 40
---

# Writing a provider in Go

This page walks through building a small custom provider end-to-end. The
provider, `echo`, has two actions:

- `echo` — returns its input attributes as outputs.
- `randomize` — takes a `prefix` and returns `<prefix>-<random-hex>`.

The source is tiny (~120 lines) but covers every part of the protocol: the
`schema` subcommand, the RPC loop, method dispatch, and error handling.

## Project layout

```
orchard-echo/
├── go.mod
└── main.go
```

```sh
mkdir orchard-echo && cd orchard-echo
go mod init github.com/you/orchard-echo
```

No dependencies — this example talks JSON directly. Providers that want to use
Orchard's types can import `github.com/pthm/orchard/pkg/protocol`.

## `main.go`

```go
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
	// Plan mode: `orchard plan` runs `<binary> schema`.
	if len(os.Args) > 1 && os.Args[1] == "schema" {
		raw, _ := json.Marshal(schema())
		fmt.Println(string(raw))
		return
	}

	// Run mode: read requests from stdin, write responses to stdout.
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
		"version":  "0.1.0",
		"protocol": "1",
		"actions": map[string]any{
			"echo": map[string]any{
				"description": "Returns input attrs as outputs",
				"attrs": map[string]any{
					"message": map[string]any{"type": "string", "required": true},
				},
				"outputs": map[string]any{"type": "any"},
			},
			"randomize": map[string]any{
				"description": "Returns <prefix>-<random-hex>",
				"attrs": map[string]any{
					"prefix": map[string]any{"type": "string", "required": true},
				},
				"outputs": map[string]any{
					"type": "object({ value = string })",
				},
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
	case "echo":
		writeResult(w, req.ID, map[string]any{"outputs": params.Attrs})
	case "randomize":
		var prefix string
		if err := json.Unmarshal(params.Attrs["prefix"], &prefix); err != nil {
			writeError(w, req.ID, "invalid_request", "prefix: "+err.Error())
			return
		}
		raw, _ := json.Marshal(fmt.Sprintf("%s-%08x", prefix, rng.Uint32()))
		writeResult(w, req.ID, map[string]any{
			"outputs": map[string]json.RawMessage{"value": raw},
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
```

Build it:

```sh
go build -o bin/orchard-echo .
```

## Test with `orchard plan`

Create a scenario that uses the provider:

```hcl
# scenarios/echo.hcl
scenario "echo_test" {
  required_providers {
    echo = { source = "exec:./bin/orchard-echo" }
  }

  action "randomize" "greeting" {
    prefix = "hello"
  }

  output "value" {
    value = action.randomize.greeting.value
  }
}
```

Plan it:

```sh
orchard plan scenarios/echo.hcl
```

Orchard runs `./bin/orchard-echo schema`, reads the schema, validates the
scenario against it, and prints the plan. **No subprocess survives past plan.**

## Test with `orchard run`

```sh
orchard run scenarios/echo.hcl
```

Orchard spawns `./bin/orchard-echo`, runs the describe/configure/execute/
shutdown handshake, and prints:

```
value = hello-3f8a2e1b
```

## What's missing from this example

This is the minimum viable provider. Real providers also need:

- **Configuration handling.** The echo provider takes no config. To accept
  config, declare a `config` block in the schema and handle the `config`
  field in the `configure` request — typically by decoding the JSON-encoded
  cty values using `pkg/protocol/ctyjson.go`.
- **Resource lifecycle.** If your provider holds a connection, a client, or
  any state, open it on `configure` and close it on `shutdown`. Return
  `configure_failed` if opening fails.
- **Validation.** Check attribute values against your business rules and
  return `validation_failed` early. Don't let bad input reach downstream
  systems.
- **Error detail.** Populate the error `detail` field with stack traces or
  API response bodies. Users will need them to debug failures.
- **Graceful shutdown.** Flush buffers, commit pending work, close
  connections. Orchard force-kills after 5 seconds.

## Using `pkg/protocol` in Go

If you're writing your provider in Go and are comfortable depending on the
Orchard module, `pkg/protocol` gives you typed envelope helpers:

```go
import "github.com/pthm/orchard/pkg/protocol"

r := bufio.NewReader(os.Stdin)
for {
    var req protocol.Request
    if err := protocol.ReadMessage(r, &req); err != nil {
        return
    }
    // dispatch on req.Method, build resp, then:
    protocol.WriteMessage(os.Stdout, resp)
}
```

This is roughly how Orchard's own external-provider client works — the wire
contract is symmetrical.

## Language-agnostic providers

The protocol has nothing Go-specific about it. A Python or Node provider that
reads JSON-lines from stdin and writes JSON-lines to stdout will work just as
well. The echo provider above is 130 lines of Go; a Python equivalent is
comparably small.

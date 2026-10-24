---
title: Writing a provider in Go
linkTitle: Writing a provider
weight: 40
---

## The quick way: `pkg/sdk`

If your provider is Go, and especially if it calls into an existing Go
codebase (create a user through your web app's own code, seed a tenant),
`github.com/pthm/orchard/pkg/sdk` turns typed functions into actions. Put a
small `main` package in the app's repo:

```go
package main

import (
	"context"
	"log"

	"github.com/pthm/orchard/pkg/sdk"

	"example.com/app/users"
)

type Config struct {
	DSN string `json:"dsn" doc:"The app's database."`
}

type CreateUser struct {
	Email string `json:"email"`
	Admin bool   `json:"admin,omitempty" doc:"Grant the admin role."`
}

func main() {
	var svc *users.Service
	p := sdk.New("app", "0.1.0")
	p.Configure(func(ctx context.Context, c Config) (err error) {
		svc, err = users.Open(ctx, c.DSN)
		return err
	})
	p.Action("app_create_user", "Create a user through the app.",
		func(ctx context.Context, in CreateUser) (*users.User, error) {
			return svc.Create(ctx, in.Email, in.Admin)
		})
	if err := p.Serve(); err != nil {
		log.Fatal(err)
	}
}
```

```hcl
required_providers {
  app = { source = "exec:./bin/orchard-app" }
}

provider "app" { dsn = var.dsn }

action "app_create_user" "alice" {
  email = "alice@example.com"
}
```

- **Functions** take a `context.Context` and optionally a struct, and return
  an `error`, optionally after a struct. The argument's fields are the action's
  attributes; the result's fields are its outputs
  (`action.app_create_user.alice.id`).
- **Names and values follow `encoding/json`**: a field's name is its json tag,
  so an app type that already has tags works as an output as it is. Types with
  a `MarshalText` method, such as `time.Time`, are strings.
- **A field is optional** when it's a pointer or tagged `omitempty`. A
  `doc:"..."` tag is its description in `orchard docs`.
- **`Serve`** answers `<binary> schema` for `orchard plan`, so plans
  type-check against your functions without starting anything. It also points
  `os.Stdout` at stderr, so stray prints from the app don't break the
  protocol.
- **Errors and panics** fail the action with their message; a panic includes
  its stack trace and leaves the provider running.
- **Teardown**: `Undo` builds the call that reverses an action from its input
  and output, so scenarios need no `lifecycle` block. Orchard records it with
  the run and `orchard teardown` runs it:

  ```go
  p.Action("app_delete_user", "Delete a user.", func(ctx context.Context, in DeleteUser) error {
  	return svc.Delete(ctx, in.ID)
  })
  p.Undo("app_create_user", "app_delete_user", func(in CreateUser, out *users.User) DeleteUser {
  	return DeleteUser{ID: out.ID}
  })
  ```

  Return a pointer and make it nil to record no undo for a run. A scenario's own
  `teardown` block, or `managed_teardown = false`, takes precedence.

The rest of this page builds a provider by hand, which is what the SDK does for
you, and what a provider in another language does.

## By hand

This page walks through building a small custom provider end-to-end. The
provider, `echo`, has two actions:

- `echo` — returns its input attributes as outputs.
- `randomize` — takes a `prefix` and returns `<prefix>-<random-hex>`.

The source is tiny (~120 lines) but covers every part of the protocol: the
`schema` subcommand, the RPC loop, method dispatch, and error handling.

### Project layout

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

### `main.go`

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

### Test with `orchard plan`

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

### Test with `orchard run`

```sh
orchard run scenarios/echo.hcl
```

Orchard spawns `./bin/orchard-echo`, runs the describe/configure/execute/
shutdown handshake, and prints:

```
value = hello-3f8a2e1b
```

### What's missing from this example

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

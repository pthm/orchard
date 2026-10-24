// Package sdk turns typed Go functions into an Orchard external provider,
// so an application can expose its own code (create a user, seed a
// tenant) as scenario actions without handling the protocol by hand.
//
//	p := sdk.New("app", "0.1.0")
//	p.Configure(func(ctx context.Context, c Config) error { ... })
//	p.Action("app_create_user", "Create a user.", func(ctx context.Context, in CreateUser) (User, error) { ... })
//	if err := p.Serve(); err != nil {
//		log.Fatal(err)
//	}
//
// Attribute and output names, and how values convert, follow
// encoding/json: a field's name is its json tag, and a type that
// implements encoding.TextMarshaler (time.Time, most ID types) is a
// string. A field is optional when it is a pointer or tagged omitempty.
// A `doc:"..."` tag becomes the attribute's description.
package sdk

import (
	"bufio"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/pthm/orchard/pkg/protocol"
)

// Provider collects the configure function and actions, and serves them.
type Provider struct {
	// Description is the provider's intro text in its schema and docs.
	Description string

	name, version string
	config        *handler
	actions       map[string]*handler
	schema        protocol.ProviderSchema
}

// handler is a registered function: func(ctx[, In]) ([Out, ]error).
type handler struct {
	fn  reflect.Value
	in  reflect.Type // nil when the function takes only a context
	out reflect.Type // nil when it returns only an error
	// outNames lists the output fields, so ones dropped by omitempty
	// are still sent, as null.
	outNames []string
}

// New returns a provider with no actions. name is the provider's name
// in its schema; scenarios pick their own local name for it.
func New(name, version string) *Provider {
	return &Provider{
		name:    name,
		version: version,
		actions: map[string]*handler{},
		schema: protocol.ProviderSchema{
			Name:     name,
			Version:  version,
			Protocol: protocol.ProtocolVersion,
			Config:   map[string]protocol.AttrSchema{},
			Actions:  map[string]protocol.ActionSchema{},
		},
	}
}

// Configure registers fn, of type func(context.Context, C) error, to
// receive the scenario's `provider` block. C is a struct whose fields are
// the block's attributes. It runs once, before any action; open
// connections there. It panics if fn has the wrong shape.
func (p *Provider) Configure(fn any) {
	h, err := newHandler(fn)
	if err == nil && h.out != nil {
		err = errors.New("must return only an error")
	}
	if err == nil && h.in == nil {
		err = errors.New("must take a config struct after the context")
	}
	var attrs map[string]protocol.AttrSchema
	if err == nil {
		attrs, err = attrSchemas(h.in)
	}
	if err != nil {
		panic(fmt.Sprintf("sdk: Configure: %v", err))
	}
	p.config = h
	p.schema.Config = attrs
}

// Action registers fn as the action type name. fn has one of these
// shapes, where In and Out are structs (or pointers to them):
//
//	func(context.Context, In) (Out, error)
//	func(context.Context, In) error
//	func(context.Context) (Out, error)
//	func(context.Context) error
//
// In's fields are the action's attributes and Out's fields its outputs.
// Name it `<provider>_<verb>`, like the builtins. It panics if fn has the
// wrong shape or name is taken.
func (p *Provider) Action(name, description string, fn any) {
	if _, ok := p.actions[name]; ok {
		panic(fmt.Sprintf("sdk: Action %q: already registered", name))
	}
	h, err := newHandler(fn)
	as := protocol.ActionSchema{Description: description, Attrs: map[string]protocol.AttrSchema{}}
	if err == nil && h.in != nil {
		as.Attrs, err = attrSchemas(h.in)
	}
	if err == nil && h.out != nil {
		var t string
		t, err = ctyType(h.out)
		as.Outputs = &protocol.AttrSchema{Type: t}
		h.outNames = jsonNames(h.out)
	}
	if err != nil {
		panic(fmt.Sprintf("sdk: Action %q: %v", name, err))
	}
	p.actions[name] = h
	p.schema.Actions[name] = as
}

// Serve runs the provider. With a `schema` argument, as `orchard plan`
// runs it, it prints the schema and returns. Otherwise it answers
// requests on stdin and stdout until Orchard shuts it down.
//
// Anything else writing to stdout would corrupt the protocol, so Serve
// points os.Stdout at stderr, which Orchard logs.
//
// ponytail: code that writes to file descriptor 1 directly, not through
// os.Stdout, still corrupts it; redirect the descriptor if that turns up.
func (p *Provider) Serve() error {
	p.schema.Description = p.Description
	if err := p.schema.Validate(); err != nil {
		return err
	}
	if len(os.Args) > 1 && os.Args[1] == "schema" {
		return json.NewEncoder(os.Stdout).Encode(p.schema)
	}
	out := os.Stdout
	os.Stdout = os.Stderr
	return p.serve(os.Stdin, out)
}

func (p *Provider) serve(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	ctx := context.Background()
	for {
		var req protocol.Request
		if err := protocol.ReadMessage(br, &req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		result, perr := p.handle(ctx, req)
		resp := protocol.Response{ID: req.ID, Error: perr}
		if perr == nil {
			raw, err := json.Marshal(result)
			if err != nil {
				return err
			}
			resp.Result = raw
		}
		if err := protocol.WriteMessage(w, resp); err != nil {
			return err
		}
		if req.Method == protocol.MethodShutdown {
			return nil
		}
	}
}

func (p *Provider) handle(ctx context.Context, req protocol.Request) (any, *protocol.Error) {
	switch req.Method {
	case protocol.MethodDescribe:
		return protocol.DescribeResult{Schema: p.schema}, nil
	case protocol.MethodConfigure:
		var params protocol.ConfigureParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &protocol.Error{Code: protocol.ErrCodeInvalidRequest, Message: err.Error()}
		}
		if p.config != nil {
			if _, perr := p.config.call(ctx, params.Config); perr != nil {
				perr.Code = protocol.ErrCodeConfigure
				return nil, perr
			}
		}
		return protocol.ConfigureResult{}, nil
	case protocol.MethodExecute:
		var params protocol.ExecuteParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &protocol.Error{Code: protocol.ErrCodeInvalidRequest, Message: err.Error()}
		}
		h, ok := p.actions[params.Action]
		if !ok {
			return nil, &protocol.Error{Code: protocol.ErrCodeUnknownAction, Message: "unknown action " + params.Action}
		}
		outputs, perr := h.call(ctx, params.Attrs)
		if perr != nil {
			return nil, perr
		}
		return protocol.ExecuteResult{Outputs: outputs}, nil
	case protocol.MethodShutdown:
		return protocol.ShutdownResult{}, nil
	}
	return nil, &protocol.Error{Code: protocol.ErrCodeUnknownMethod, Message: "unknown method " + req.Method}
}

var (
	ctxType   = reflect.TypeFor[context.Context]()
	errType   = reflect.TypeFor[error]()
	textType  = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMType = reflect.TypeFor[json.Marshaler]()
)

func newHandler(fn any) (*handler, error) {
	v := reflect.ValueOf(fn)
	t := v.Type()
	if t.Kind() != reflect.Func {
		return nil, fmt.Errorf("want a function, got %s", t)
	}
	if t.NumIn() < 1 || t.NumIn() > 2 || t.In(0) != ctxType {
		return nil, fmt.Errorf("%s: want a context.Context and optionally a struct as arguments", t)
	}
	if t.NumOut() < 1 || t.NumOut() > 2 || t.Out(t.NumOut()-1) != errType {
		return nil, fmt.Errorf("%s: want an error, optionally after a struct, as results", t)
	}
	h := &handler{fn: v}
	if t.NumIn() == 2 {
		h.in = t.In(1)
		if !isStruct(h.in) {
			return nil, fmt.Errorf("%s: argument must be a struct, got %s", t, h.in)
		}
	}
	if t.NumOut() == 2 {
		h.out = t.Out(0)
		if !isStruct(h.out) {
			return nil, fmt.Errorf("%s: result must be a struct, got %s", t, h.out)
		}
	}
	return h, nil
}

// call decodes attrs into the function's argument, calls it, and encodes
// its result as outputs. A panic in fn fails the call rather than the
// process.
func (h *handler) call(ctx context.Context, attrs map[string]json.RawMessage) (outputs map[string]json.RawMessage, perr *protocol.Error) {
	defer func() {
		if r := recover(); r != nil {
			perr = &protocol.Error{Code: protocol.ErrCodeInternal, Message: fmt.Sprint("panic: ", r), Detail: string(debug.Stack())}
		}
	}()
	args := []reflect.Value{reflect.ValueOf(ctx)}
	if h.in != nil {
		raw, err := json.Marshal(attrs)
		if err != nil {
			return nil, &protocol.Error{Code: protocol.ErrCodeInvalidRequest, Message: err.Error()}
		}
		in := reflect.New(h.in)
		if err := json.Unmarshal(raw, in.Interface()); err != nil {
			return nil, &protocol.Error{Code: protocol.ErrCodeValidation, Message: err.Error()}
		}
		args = append(args, in.Elem())
	}
	res := h.fn.Call(args)
	if err, _ := res[len(res)-1].Interface().(error); err != nil {
		return nil, &protocol.Error{Code: protocol.ErrCodeExecute, Message: err.Error()}
	}
	outputs = map[string]json.RawMessage{}
	if h.out == nil {
		return outputs, nil
	}
	if out := res[0]; out.Kind() != reflect.Pointer || !out.IsNil() {
		raw, err := json.Marshal(out.Interface())
		if err != nil {
			return nil, &protocol.Error{Code: protocol.ErrCodeExecute, Message: "encoding outputs: " + err.Error()}
		}
		if err := json.Unmarshal(raw, &outputs); err != nil {
			return nil, &protocol.Error{Code: protocol.ErrCodeExecute, Message: "outputs must encode as a JSON object: " + err.Error()}
		}
	}
	for _, n := range h.outNames {
		if _, ok := outputs[n]; !ok {
			outputs[n] = json.RawMessage("null")
		}
	}
	return outputs, nil
}

func isStruct(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct && !isText(t)
}

func isText(t reflect.Type) bool {
	return t.Implements(textType) || reflect.PointerTo(t).Implements(textType)
}

// field is a struct field as encoding/json sees it.
type field struct {
	name     string
	typ      reflect.Type
	optional bool
	doc      string
}

// jsonFields lists t's fields under their encoding/json names: the json
// tag's name, or the Go name; `-` and unexported fields are left out, and
// untagged embedded structs are flattened.
//
// ponytail: when two promoted fields share a name, encoding/json drops
// both, but this keeps the first; the engine then sends an attribute
// nothing reads.
func jsonFields(t reflect.Type) []field {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var fields []field
	seen := map[string]bool{}
	for _, f := range reflect.VisibleFields(t) {
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && isStruct(f.Type) {
			continue // its fields are promoted
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		fields = append(fields, field{
			name:     name,
			typ:      f.Type,
			optional: f.Type.Kind() == reflect.Pointer || strings.Contains(","+opts+",", ",omitempty,"),
			doc:      f.Tag.Get("doc"),
		})
	}
	return fields
}

func jsonNames(t reflect.Type) []string {
	var names []string
	for _, f := range jsonFields(t) {
		names = append(names, f.name)
	}
	return names
}

func attrSchemas(t reflect.Type) (map[string]protocol.AttrSchema, error) {
	attrs := map[string]protocol.AttrSchema{}
	for _, f := range jsonFields(t) {
		ct, err := ctyType(f.typ)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.name, err)
		}
		attrs[f.name] = protocol.AttrSchema{Type: ct, Required: !f.optional, Description: f.doc}
	}
	return attrs, nil
}

// ctyType is the cty type expression for values of t, as encoding/json
// encodes them.
func ctyType(t reflect.Type) (string, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case isText(t):
		return "string", nil
	case t.Implements(jsonMType) || reflect.PointerTo(t).Implements(jsonMType):
		return "any", nil
	}
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "bool", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number", nil
	case reflect.Interface:
		return "any", nil
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return "string", nil // base64, as encoding/json writes []byte
		}
		et, err := ctyType(t.Elem())
		return "list(" + et + ")", err
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return "", fmt.Errorf("map keys must be strings, got %s", t.Key())
		}
		et, err := ctyType(t.Elem())
		return "map(" + et + ")", err
	case reflect.Struct:
		fields := jsonFields(t)
		parts := make([]string, 0, len(fields))
		for _, f := range fields {
			ft, err := ctyType(f.typ)
			if err != nil {
				return "", fmt.Errorf("%s: %w", f.name, err)
			}
			if f.optional {
				ft = "optional(" + ft + ")"
			}
			parts = append(parts, f.name+"="+ft)
		}
		sort.Strings(parts)
		return "object({" + strings.Join(parts, ", ") + "})", nil
	}
	return "", fmt.Errorf("unsupported type %s", t)
}

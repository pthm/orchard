// Package grpc is the builtin Orchard provider for gRPC services. It
// calls unary methods with requests written as HCL objects, learning
// the services' shapes from server reflection or a descriptor set, so
// there are no generated stubs.
package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/zclconf/go-cty/cty"
	_ "google.golang.org/genproto/googleapis/rpc/errdetails" // registers google.rpc detail types, for status errors
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/snapshot"
)

//go:embed schema.hcl
var schemaHCL []byte

// Provider is the gRPC provider implementation.
//
// A zero Provider is valid but not yet usable — Configure must be
// called before Execute. Schema works on a zero Provider so the engine
// can validate scenarios before connecting.
type Provider struct {
	schema protocol.ProviderSchema

	address       string
	descriptorSet string
	metadata      map[string]string
	conn          *grpc.ClientConn

	mu    sync.Mutex
	descs *descs // loaded on first use, then cached for the run
}

// New returns an unconfigured gRPC provider.
func New() *Provider {
	return &Provider{schema: protocol.MustParseHCL(schemaHCL)}
}

var (
	_ provider.Provider       = (*Provider)(nil)
	_ provider.ActionPlanner  = (*Provider)(nil)
	_ provider.PlanConfigurer = (*Provider)(nil)
	_ provider.SchemaSource   = (*Provider)(nil)
)

// Schema returns the embedded provider schema.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

func str(cfg map[string]cty.Value, name string) string {
	if v, ok := cfg[name]; ok && v.IsKnown() && !v.IsNull() && v.Type() == cty.String {
		return v.AsString()
	}
	return ""
}

func stringMap(v cty.Value) map[string]string {
	if !v.IsKnown() || v.IsNull() || v.LengthInt() == 0 {
		return nil
	}
	out := map[string]string{}
	for k, e := range v.AsValueMap() {
		out[k] = e.AsString()
	}
	return out
}

// Configure creates the client. Connecting is lazy: the first call (or
// the reflection request before the run) reports an unreachable
// server.
func (p *Provider) Configure(_ context.Context, cfg map[string]cty.Value) error {
	p.address = str(cfg, "address")
	if p.address == "" {
		return fmt.Errorf("grpc: address is required")
	}
	p.descriptorSet = str(cfg, "descriptor_set")
	p.metadata = stringMap(cfg["default_metadata"])

	creds := insecure.NewCredentials()
	if v := cfg["tls"]; v.IsKnown() && !v.IsNull() && v.True() {
		tc := &tls.Config{ServerName: str(cfg, "server_name"), MinVersion: tls.VersionTLS12}
		if ca := str(cfg, "ca_file"); ca != "" {
			pem, err := os.ReadFile(ca)
			if err != nil {
				return fmt.Errorf("grpc: ca_file: %w", err)
			}
			tc.RootCAs = x509.NewCertPool()
			if !tc.RootCAs.AppendCertsFromPEM(pem) {
				return fmt.Errorf("grpc: ca_file %s has no PEM certificates", ca)
			}
		}
		creds = credentials.NewTLS(tc)
	}
	conn, err := grpc.NewClient(p.address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return fmt.Errorf("grpc: %w", err)
	}
	p.conn = conn
	return nil
}

// loadDescs returns the descriptors: from descriptor_set when it's set,
// otherwise from server reflection. They're fetched once per run.
func (p *Provider) loadDescs(ctx context.Context) (*descs, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.descs != nil {
		return p.descs, nil
	}
	var (
		d   *descs
		err error
	)
	switch {
	case p.descriptorSet != "":
		d, err = readDescriptorSet(p.descriptorSet)
	case p.conn != nil:
		d, err = reflect(ctx, p.conn, p.address)
	default:
		return nil, fmt.Errorf("no descriptors: %w", provider.ErrNoSchema)
	}
	if err != nil {
		return nil, err
	}
	p.descs = d
	return d, nil
}

// Execute dispatches to the handler for the given action.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if p.conn == nil {
		return nil, fmt.Errorf("grpc: not configured")
	}
	if action != "grpc_call" {
		return nil, fmt.Errorf("grpc: unknown action %q", action)
	}
	out, err := p.call(ctx, attrs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	return out, nil
}

func (p *Provider) call(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	d, err := p.loadDescs(ctx)
	if err != nil {
		return nil, err
	}
	md, err := d.method(attrs["method"].AsString())
	if err != nil {
		return nil, err
	}
	c := conv{types: d.types}
	req := dynamicpb.NewMessage(md.Input())
	if err := c.fill(req, attrs["request"], ""); err != nil {
		return nil, err
	}

	pairs := map[string]string{}
	for k, v := range p.metadata {
		pairs[k] = v
	}
	for k, v := range stringMap(attrs["metadata"]) {
		pairs[k] = v
	}
	if len(pairs) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, metadata.New(pairs))
	}

	resp := dynamicpb.NewMessage(md.Output())
	var header, trailer metadata.MD
	fullMethod := fmt.Sprintf("/%s/%s", md.Parent().FullName(), md.Name())
	if err := p.conn.Invoke(ctx, fullMethod, req, resp, grpc.Header(&header), grpc.Trailer(&trailer)); err != nil {
		return nil, statusError(err, d.types)
	}
	return map[string]cty.Value{
		"response": c.messageValue(resp),
		"headers":  metadataValue(header),
		"trailers": metadataValue(trailer),
	}, nil
}

// statusError renders a failed call as `CODE: message`, with any
// google.rpc.Status details as JSON.
func statusError(err error, types *dynamicpb.Types) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	msg := fmt.Sprintf("%s: %s", codeName(st.Code().String()), st.Message())
	for _, d := range st.Proto().GetDetails() {
		b, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(d)
		if err != nil {
			b, err = (protojson.MarshalOptions{UseProtoNames: true, Resolver: types}).Marshal(d)
		}
		if err != nil {
			msg += " " + d.GetTypeUrl()
			continue
		}
		var compact json.RawMessage = b
		if c, err := json.Marshal(compact); err == nil {
			b = c
		}
		msg += " " + string(b)
	}
	return errors.New(msg)
}

// codeName turns grpc-go's code names (NotFound) into the canonical
// ones (NOT_FOUND).
func codeName(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(rune(s[i-1])) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// metadataValue keeps the first value per key, as http_request does.
// Binary (-bin) values are base64-encoded.
func metadataValue(md metadata.MD) cty.Value {
	if len(md) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	out := map[string]cty.Value{}
	for k, vs := range md {
		if len(vs) == 0 {
			continue
		}
		v := vs[0]
		if strings.HasSuffix(k, "-bin") {
			v = base64.StdEncoding.EncodeToString([]byte(v))
		}
		out[k] = cty.StringVal(v)
	}
	return cty.MapVal(out)
}

// PlanAction checks the method and request against the descriptors and
// returns the response's type, so references to response fields are
// type-checked. At plan they come from descriptor_set or the snapshot;
// before a run, from descriptor_set or the live server.
func (p *Provider) PlanAction(ctx context.Context, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	if action != "grpc_call" {
		return provider.Plan{}, nil
	}
	method := attrs["method"]
	if !method.IsKnown() || method.IsNull() {
		return provider.Plan{}, nil
	}
	d, err := p.loadDescs(ctx)
	if err != nil {
		return provider.Plan{}, err
	}
	md, err := d.method(method.AsString())
	if err != nil {
		return provider.Plan{}, err
	}
	if err := (conv{types: d.types}).fill(dynamicpb.NewMessage(md.Input()), attrs["request"], ""); err != nil {
		return provider.Plan{}, err
	}
	return provider.Plan{Outputs: cty.Object(map[string]cty.Type{
		"response": messageType(md.Output()),
		"headers":  cty.Map(cty.String),
		"trailers": cty.Map(cty.String),
	})}, nil
}

// snapshotKind is the Capture kind of a gRPC snapshot.
const snapshotKind = "protobuf"

// snapshotBody is the body of a protobuf snapshot. The descriptor set
// is protojson, not binary, so schema changes diff readably.
type snapshotBody struct {
	Services          []string        `json:"services"`
	FileDescriptorSet json.RawMessage `json:"file_descriptor_set"`
}

// PlanConfigure loads the descriptors `orchard plan` checks calls
// against: descriptor_set when it's configured, otherwise the snapshot.
// With neither, calls go unchecked at plan. It doesn't connect.
func (p *Provider) PlanConfigure(_ context.Context, cfg map[string]cty.Value, snap *snapshot.Snapshot) error {
	p.address = str(cfg, "address")
	p.descriptorSet = str(cfg, "descriptor_set")
	if p.descriptorSet != "" || snap == nil {
		return nil
	}
	if snap.Kind != snapshotKind {
		return fmt.Errorf("snapshot is a %q snapshot, not %q", snap.Kind, snapshotKind)
	}
	var body snapshotBody
	if err := json.Unmarshal(snap.Body, &body); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	var set descriptorpb.FileDescriptorSet
	if err := protojson.Unmarshal(body.FileDescriptorSet, &set); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	d, err := newDescs(&set)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	p.mu.Lock()
	p.descs = d
	p.mu.Unlock()
	return nil
}

// PullSchema captures the descriptors of every service, from
// descriptor_set or reflection, for `orchard schema pull`.
func (p *Provider) PullSchema(ctx context.Context, _ provider.PullOptions) (*provider.Capture, error) {
	d, err := p.loadDescs(ctx)
	if err != nil {
		return nil, err
	}
	set, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(d.set)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(snapshotBody{Services: d.services, FileDescriptorSet: set})
	if err != nil {
		return nil, err
	}
	return &provider.Capture{Kind: snapshotKind, Target: "grpc/" + p.address, Body: body}, nil
}

// Close closes the connection. Safe to call on an unconfigured provider
// or more than once.
func (p *Provider) Close() error {
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn = nil
	return err
}

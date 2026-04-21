// Package http is the builtin Orchard provider for HTTP requests.
//
// Exposes a single action — "request" — that issues an HTTP call
// and captures the response as structured output (status, body,
// headers). Intended as a thin, predictable wrapper over net/http,
// not a rich client with retries or circuit breakers. Scenarios
// that need those compose them via dependency ordering or wait
// for the future retry/timeout features at the action level.
package http

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

//go:embed schema.hcl
var schemaHCL []byte

// Provider is the HTTP provider implementation.
type Provider struct {
	schema         protocol.ProviderSchema
	client         *nethttp.Client
	baseURL        *url.URL
	defaultHeaders map[string]string
}

// New returns an unconfigured HTTP provider. Schema is parsed once
// at construction so any authoring error in the embedded schema.hcl
// fails loudly on startup rather than at first use.
func New() *Provider {
	return &Provider{schema: protocol.MustParseHCL(schemaHCL)}
}

var _ provider.Provider = (*Provider)(nil)

// Schema returns the embedded provider schema.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

// Configure reads base_url, default_headers, and timeout_seconds
// from the scenario's provider block and constructs the shared
// http.Client. The timeout applies to the full request/response
// round-trip.
func (p *Provider) Configure(ctx context.Context, config map[string]cty.Value) error {
	timeout := 30 * time.Second
	if v, ok := config["timeout_seconds"]; ok && !v.IsNull() {
		if v.Type() != cty.Number {
			return fmt.Errorf("http: timeout_seconds must be a number, got %s", v.Type().FriendlyName())
		}
		secs, _ := v.AsBigFloat().Float64()
		timeout = time.Duration(secs * float64(time.Second))
	}

	if v, ok := config["base_url"]; ok && !v.IsNull() {
		if v.Type() != cty.String {
			return fmt.Errorf("http: base_url must be a string, got %s", v.Type().FriendlyName())
		}
		parsed, err := url.Parse(v.AsString())
		if err != nil {
			return fmt.Errorf("http: base_url: %w", err)
		}
		p.baseURL = parsed
	}

	if v, ok := config["default_headers"]; ok && !v.IsNull() {
		if !v.Type().IsMapType() && !v.Type().IsObjectType() {
			return fmt.Errorf("http: default_headers must be a map, got %s", v.Type().FriendlyName())
		}
		p.defaultHeaders = ctyMapToStringMap(v)
	}

	p.client = &nethttp.Client{Timeout: timeout}
	return nil
}

// Execute dispatches to the "request" action. Unknown actions are
// rejected — the schema declares only "request", and the engine
// should have caught anything else at route-building time.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if p.client == nil {
		return nil, fmt.Errorf("http: not configured")
	}
	switch action {
	case "http_request":
		return p.executeRequest(ctx, attrs)
	default:
		return nil, fmt.Errorf("http: unknown action %q", action)
	}
}

// Close has nothing to release — net/http.Client owns its own
// idle-connection pool and cleans up via Go's runtime.
func (p *Provider) Close() error { return nil }

func (p *Provider) executeRequest(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	method := "GET"
	if v, ok := attrs["method"]; ok && !v.IsNull() {
		method = strings.ToUpper(v.AsString())
	}

	rawURL := attrs["url"].AsString()
	resolved, err := p.resolveURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("http.request: url: %w", err)
	}

	var body io.Reader
	if v, ok := attrs["body"]; ok && !v.IsNull() {
		body = strings.NewReader(v.AsString())
	}

	req, err := nethttp.NewRequestWithContext(ctx, method, resolved, body)
	if err != nil {
		return nil, fmt.Errorf("http.request: build: %w", err)
	}

	// Default headers first, per-request headers override.
	for k, v := range p.defaultHeaders {
		req.Header.Set(k, v)
	}
	if v, ok := attrs["headers"]; ok && !v.IsNull() {
		for k, val := range ctyMapToStringMap(v) {
			req.Header.Set(k, val)
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http.request: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http.request: read body: %w", err)
	}

	// Collapse multi-valued response headers to their first entry.
	// This matches the schema's output type (map(string)); callers
	// that need all values can use the raw transport when we add
	// that escape hatch.
	headers := make(map[string]cty.Value, len(resp.Header))
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = cty.StringVal(v[0])
		}
	}

	outputs := map[string]cty.Value{
		"status":  cty.NumberIntVal(int64(resp.StatusCode)),
		"body":    cty.StringVal(string(respBody)),
		"headers": ctyStringMap(headers),
	}
	return outputs, nil
}

// resolveURL combines base_url (if set) with the URL supplied in
// the action. Absolute URLs in the action bypass base_url — the
// common case for scenarios that mix calls to several hosts.
func (p *Provider) resolveURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() || p.baseURL == nil {
		return raw, nil
	}
	// Resolve relative path against base_url.
	return p.baseURL.ResolveReference(parsed).String(), nil
}

// ctyStringMap wraps a map of cty values as a cty map value. Empty
// maps require an explicit type since MapValEmpty needs one; we
// pick cty.String since headers and config are always string-valued.
func ctyStringMap(m map[string]cty.Value) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	return cty.MapVal(m)
}

// ctyMapToStringMap flattens a cty map/object value into a Go
// map[string]string. Non-string values are coerced via GoString to
// avoid introducing a conversion error for the common typo of
// `headers = { ..., timeout = 5 }` — the server will interpret it,
// not us.
func ctyMapToStringMap(v cty.Value) map[string]string {
	out := make(map[string]string)
	if v.IsNull() {
		return out
	}
	it := v.ElementIterator()
	for it.Next() {
		k, val := it.Element()
		out[k.AsString()] = ctyToString(val)
	}
	return out
}

// ctyToString renders a cty value for inclusion in a string-valued
// context (like an HTTP header). It's a best-effort — use the
// schema's own type coercion when strict behaviour is needed.
func ctyToString(v cty.Value) string {
	if v.IsNull() {
		return ""
	}
	switch v.Type() {
	case cty.String:
		return v.AsString()
	case cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			i, _ := bf.Int64()
			return fmt.Sprintf("%d", i)
		}
		f, _ := bf.Float64()
		return fmt.Sprintf("%g", f)
	case cty.Bool:
		if v.True() {
			return "true"
		}
		return "false"
	}
	return v.GoString()
}

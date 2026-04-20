package http

import (
	"context"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// configured returns an HTTP provider pointing at srv, ready for
// Execute. Tests that care about config variants construct their
// own.
func configured(t *testing.T, srv *httptest.Server) *Provider {
	t.Helper()
	p := New()
	if err := p.Configure(context.Background(), map[string]cty.Value{
		"base_url": cty.StringVal(srv.URL),
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestSchemaLoadsFromEmbeddedHCL(t *testing.T) {
	p := New()
	s := p.Schema()
	if s.Name != "http" {
		t.Fatalf("name: got %q", s.Name)
	}
	if _, ok := s.Actions["http_request"]; !ok {
		t.Fatal("missing request action")
	}
	if s.Actions["http_request"].Attrs["url"].Type != "string" {
		t.Errorf("url type: %q", s.Actions["http_request"].Attrs["url"].Type)
	}
}

func TestExecuteHappyPath(t *testing.T) {
	srv := httptest.NewServer(httpHandler(t, func(w io.Writer) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	p := configured(t, srv)
	out, err := p.Execute(context.Background(), "http_request", map[string]cty.Value{
		"url": cty.StringVal("/users/1"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	status, _ := out["status"].AsBigFloat().Int64()
	if status != 200 {
		t.Errorf("status: got %d", status)
	}
	if out["body"].AsString() != `{"ok":true}` {
		t.Errorf("body: got %q", out["body"].AsString())
	}
}

func TestExecuteAbsoluteURLBypassesBaseURL(t *testing.T) {
	// Absolute URLs in the request must be used verbatim, even when
	// a base_url is configured. Tests by pointing base_url at an
	// unreachable host and using an absolute URL to the real test
	// server.
	real := httptest.NewServer(httpHandler(t, func(w io.Writer) {
		io.WriteString(w, "absolute")
	}))
	defer real.Close()

	p := New()
	if err := p.Configure(context.Background(), map[string]cty.Value{
		"base_url": cty.StringVal("http://127.0.0.1:1"), // unreachable
	}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	out, err := p.Execute(context.Background(), "http_request", map[string]cty.Value{
		"url": cty.StringVal(real.URL + "/"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["body"].AsString() != "absolute" {
		t.Errorf("body: got %q", out["body"].AsString())
	}
}

func TestExecutePOSTWithBody(t *testing.T) {
	var gotMethod, gotBody string
	srv := httptest.NewServer(httpHandler(t, func(w io.Writer) {
		io.WriteString(w, "ok")
	}).withRequestCapture(&gotMethod, &gotBody))
	defer srv.Close()

	p := configured(t, srv)
	_, err := p.Execute(context.Background(), "http_request", map[string]cty.Value{
		"method": cty.StringVal("POST"),
		"url":    cty.StringVal("/submit"),
		"body":   cty.StringVal(`{"name":"acme"}`),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method: got %q", gotMethod)
	}
	if gotBody != `{"name":"acme"}` {
		t.Errorf("body: got %q", gotBody)
	}
}

func TestExecuteHeadersMergeDefaults(t *testing.T) {
	var gotHeaders map[string]string
	srv := httptest.NewServer(httpHandler(t, func(w io.Writer) {
		io.WriteString(w, "ok")
	}).withHeaderCapture(&gotHeaders))
	defer srv.Close()

	p := New()
	if err := p.Configure(context.Background(), map[string]cty.Value{
		"base_url": cty.StringVal(srv.URL),
		"default_headers": cty.MapVal(map[string]cty.Value{
			"X-Default": cty.StringVal("from-config"),
			"X-Override": cty.StringVal("default-value"),
		}),
	}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	_, err := p.Execute(context.Background(), "http_request", map[string]cty.Value{
		"url": cty.StringVal("/"),
		"headers": cty.MapVal(map[string]cty.Value{
			"X-Override": cty.StringVal("per-request-wins"),
			"X-Extra":    cty.StringVal("only-here"),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotHeaders["X-Default"] != "from-config" {
		t.Errorf("X-Default: got %q", gotHeaders["X-Default"])
	}
	if gotHeaders["X-Override"] != "per-request-wins" {
		t.Errorf("X-Override should be overridden; got %q", gotHeaders["X-Override"])
	}
	if gotHeaders["X-Extra"] != "only-here" {
		t.Errorf("X-Extra: got %q", gotHeaders["X-Extra"])
	}
}

func TestExecuteResponseHeaders(t *testing.T) {
	srv := httptest.NewServer(httpHandler(t, func(w io.Writer) {
		io.WriteString(w, "hi")
	}).withResponseHeader("X-Custom", "v1"))
	defer srv.Close()

	p := configured(t, srv)
	out, err := p.Execute(context.Background(), "http_request", map[string]cty.Value{
		"url": cty.StringVal("/"),
	})
	if err != nil {
		t.Fatal(err)
	}
	headers := out["headers"]
	if !headers.Type().IsMapType() {
		t.Fatalf("headers should be a map, got %s", headers.Type().FriendlyName())
	}
	custom := headers.Index(cty.StringVal("X-Custom"))
	if custom.AsString() != "v1" {
		t.Errorf("X-Custom: got %q", custom.AsString())
	}
}

func TestExecuteRejectsBeforeConfigure(t *testing.T) {
	p := New()
	_, err := p.Execute(context.Background(), "http_request", map[string]cty.Value{
		"url": cty.StringVal("http://example"),
	})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured, got %v", err)
	}
}

func TestExecuteRejectsUnknownAction(t *testing.T) {
	srv := httptest.NewServer(httpHandler(t, func(w io.Writer) { io.WriteString(w, "") }))
	defer srv.Close()

	p := configured(t, srv)
	_, err := p.Execute(context.Background(), "imaginary", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown-action, got %v", err)
	}
}

func TestCtyToString(t *testing.T) {
	cases := []struct {
		in   cty.Value
		want string
	}{
		{cty.StringVal("hello"), "hello"},
		{cty.NumberIntVal(42), "42"},
		{cty.NumberFloatVal(3.5), "3.5"},
		{cty.BoolVal(true), "true"},
		{cty.BoolVal(false), "false"},
		{cty.NullVal(cty.String), ""},
	}
	for _, c := range cases {
		if got := ctyToString(c.in); got != c.want {
			t.Errorf("%#v: got %q want %q", c.in, got, c.want)
		}
	}
}

// --- httptest helpers -----------------------------------------

// httpHandlerBuilder is a tiny fluent fixture for httptest servers.
// It keeps the test body focused on what the test wants to assert
// rather than boilerplate around request parsing.
type httpHandlerBuilder struct {
	t            *testing.T
	writeBody    func(io.Writer)
	captureM     *string
	captureB     *string
	captureH     *map[string]string
	extraHeaders map[string]string
}

func httpHandler(t *testing.T, writeBody func(io.Writer)) *httpHandlerBuilder {
	return &httpHandlerBuilder{t: t, writeBody: writeBody}
}

func (b *httpHandlerBuilder) withRequestCapture(method, body *string) *httpHandlerBuilder {
	b.captureM = method
	b.captureB = body
	return b
}

func (b *httpHandlerBuilder) withHeaderCapture(h *map[string]string) *httpHandlerBuilder {
	b.captureH = h
	return b
}

func (b *httpHandlerBuilder) withResponseHeader(k, v string) *httpHandlerBuilder {
	if b.extraHeaders == nil {
		b.extraHeaders = map[string]string{}
	}
	b.extraHeaders[k] = v
	return b
}

// ServeHTTP implements http.Handler so builder can be passed to
// httptest.NewServer directly.
func (b *httpHandlerBuilder) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	if b.captureM != nil {
		*b.captureM = r.Method
	}
	if b.captureB != nil {
		body, _ := io.ReadAll(r.Body)
		*b.captureB = string(body)
	}
	if b.captureH != nil {
		captured := map[string]string{}
		for k := range r.Header {
			captured[k] = r.Header.Get(k)
		}
		*b.captureH = captured
	}
	for k, v := range b.extraHeaders {
		w.Header().Set(k, v)
	}
	b.writeBody(w)
}

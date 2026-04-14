package integration_test

import (
	"io"
	"net/http"
)

// httpFixture is a tiny ServeHTTP handler used by the HTTP scenario
// integration test. It dispatches by path to route-specific handlers
// so each test can inject its own response logic without spinning up
// a separate server per scenario step.
type httpFixture struct {
	onFetch  func(*httpFixtureRequest) httpFixtureResponse
	onSubmit func(*httpFixtureRequest) httpFixtureResponse
}

type httpFixtureRequest struct {
	method  string
	headers map[string]string
	body    []byte
}

type httpFixtureResponse struct {
	status int
	body   string
}

func (f *httpFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := &httpFixtureRequest{
		method:  r.Method,
		headers: flattenHeaders(r.Header),
	}
	if r.Body != nil {
		req.body, _ = io.ReadAll(r.Body)
	}

	var resp httpFixtureResponse
	switch r.URL.Path {
	case "/ping":
		resp = f.onFetch(req)
	case "/submit":
		resp = f.onSubmit(req)
	default:
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(resp.status)
	_, _ = io.WriteString(w, resp.body)
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k := range h {
		out[k] = h.Get(k)
	}
	return out
}

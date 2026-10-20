package apptest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Client is an HTTP helper bound to a Harness's loopback server. It fails the
// test on transport errors so call sites stay flat.
type Client struct {
	t      testing.TB
	base   string
	client *http.Client
	// headers are sent on every request (e.g. seeded dev-manager credentials a
	// test wires via WithHeader).
	headers http.Header
}

// WithHeader returns a shallow copy of the Client that also sends key:value on
// every request — handy for attaching the seeded session credentials once.
func (c *Client) WithHeader(key, value string) *Client {
	clone := *c
	clone.headers = http.Header{}
	for k, v := range c.headers {
		clone.headers[k] = v
	}
	clone.headers.Set(key, value)
	return &clone
}

// GET issues a GET to path (joined to the server base URL).
func (c *Client) GET(path string) *Response {
	c.t.Helper()
	return c.do(http.MethodGet, path, "", nil)
}

// POST issues a POST with the given body (Content-Type left to the caller via
// contentType; pass "" for none).
func (c *Client) POST(path, contentType string, body string) *Response {
	c.t.Helper()
	return c.do(http.MethodPost, path, contentType, strings.NewReader(body))
}

// GraphQL POSTs an application/json GraphQL query envelope to /api/graphql.
func (c *Client) GraphQL(query string) *Response {
	c.t.Helper()
	body, err := json.Marshal(struct {
		Query string `json:"query"`
	}{Query: query})
	if err != nil {
		c.t.Fatalf("apptest: marshal graphql query: %v", err)
	}
	return c.do(http.MethodPost, "/api/graphql", "application/json", strings.NewReader(string(body)))
}

func (c *Client) do(method, path, contentType string, body io.Reader) *Response {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		c.t.Fatalf("apptest: new request %s %s: %v", method, path, err)
	}
	for k, v := range c.headers {
		for _, vv := range v {
			req.Header.Add(k, vv)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("apptest: %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("apptest: read body %s %s: %v", method, path, err)
	}
	return &Response{t: c.t, Status: resp.StatusCode, body: raw}
}

// Response wraps a completed HTTP response with test-friendly accessors.
type Response struct {
	t      testing.TB
	Status int
	body   []byte
}

// Body returns the response body as a string.
func (r *Response) Body() string { return string(r.body) }

// JSON decodes the response body into dst, failing the test on error.
func (r *Response) JSON(dst any) {
	r.t.Helper()
	if err := json.Unmarshal(r.body, dst); err != nil {
		r.t.Fatalf("apptest: decode JSON body (status %d): %v\n%s", r.Status, err, r.body)
	}
}

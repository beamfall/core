package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestServeMCP (CORE-2098): the stdio server answers initialize, lists its
// five tools, proxies tool calls to /api/graphql with the bearer token, turns a
// server refusal into an isError tool result, and never echoes the token.
func TestServeMCP(t *testing.T) {
	token := strings.Repeat("t", 12) + "-never-echoed"
	var gotQueries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/api/graphql":
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotQueries = append(gotQueries, body.Query)
			switch {
			case strings.Contains(body.Query, "agentCatalog"):
				_, _ = io.WriteString(w, `{"data":{"agentCatalog":{"guide":"g","fields":[]}}}`)
			case strings.Contains(body.Query, "serverLogs"):
				if body.Variables["level"] != "ERROR" {
					t.Errorf("logs variables = %#v", body.Variables)
				}
				_, _ = io.WriteString(w, `{"data":{"serverLogs":{"available":true,"capacity":10,"lines":["level=ERROR"]}}}`)
			default:
				_, _ = io.WriteString(w, `{"errors":[{"message":"admin session required"}]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"catalog","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"logs","arguments":{"level":"ERROR"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"graphql","arguments":{"query":"mutation { createApiToken(name: \"x\") { id } }"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"health","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"bogus"}`,
		`not json`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(t.Context(), cliHTTPOptions{server: server.URL, token: token}, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), token) {
		t.Fatal("mcp output contains the API token")
	}

	type reply struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *mcpError       `json:"error"`
	}
	var replies []reply
	decoder := json.NewDecoder(&out)
	for decoder.More() {
		var r reply
		if err := decoder.Decode(&r); err != nil {
			t.Fatal(err)
		}
		replies = append(replies, r)
	}
	if len(replies) != 9 {
		t.Fatalf("got %d replies, want 9 (the notification gets none)", len(replies))
	}
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	_ = json.Unmarshal(replies[0].Result, &initResult)
	if initResult.ProtocolVersion != "2025-03-26" || initResult.ServerInfo.Name != "beamfall" {
		t.Fatalf("initialize = %s", replies[0].Result)
	}
	var list struct {
		Tools []mcpTool `json:"tools"`
	}
	_ = json.Unmarshal(replies[1].Result, &list)
	if len(list.Tools) != 5 {
		t.Fatalf("tools/list = %s", replies[1].Result)
	}
	toolResult := func(r reply) mcpToolResult {
		t.Helper()
		var result mcpToolResult
		if err := json.Unmarshal(r.Result, &result); err != nil || len(result.Content) != 1 {
			t.Fatalf("tool result %s: %v", r.Result, err)
		}
		return result
	}
	if got := toolResult(replies[2]); got.IsError || !strings.Contains(got.Content[0].Text, `"agentCatalog"`) {
		t.Fatalf("catalog = %+v", got)
	}
	if got := toolResult(replies[3]); got.IsError || !strings.Contains(got.Content[0].Text, "level=ERROR") {
		t.Fatalf("logs = %+v", got)
	}
	if got := toolResult(replies[4]); !got.IsError || got.Content[0].Text != "admin session required" {
		t.Fatalf("refused graphql = %+v", got)
	}
	if got := toolResult(replies[5]); got.IsError || !strings.Contains(got.Content[0].Text, "200 OK") {
		t.Fatalf("health = %+v", got)
	}
	if replies[6].Error == nil || replies[6].Error.Code != -32602 {
		t.Fatalf("unknown tool = %+v", replies[6])
	}
	if replies[7].Error == nil || replies[7].Error.Code != -32601 {
		t.Fatalf("unknown method = %+v", replies[7])
	}
	if replies[8].Error == nil || replies[8].Error.Code != -32700 {
		t.Fatalf("parse error = %+v", replies[8])
	}
	if len(gotQueries) != 3 {
		t.Fatalf("graphql calls = %d, want 3", len(gotQueries))
	}
}

// TestServeMCPReturnsOnCancelWhileIdle: a client that holds stdin open must not
// keep the server running after its context is cancelled.
func TestServeMCPReturnsOnCancelWhileIdle(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveMCP(ctx, cliHTTPOptions{server: "http://127.0.0.1:1"}, reader, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveMCP after cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveMCP did not return after its context was cancelled")
	}
}

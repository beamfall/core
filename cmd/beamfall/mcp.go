package main

// `beamfall mcp` (CORE-2098): a Model Context Protocol server over stdio that
// proxies an agent's tool calls to a running Beamfall server's private
// /api/graphql surface with an operator API token. It speaks newline-delimited
// JSON-RPC 2.0 and adds no dependency. The token is sent only in the
// Authorization header; it is never written to stdout or stderr.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/beamfall/core/internal/capabilities"
)

const mcpProtocolVersion = "2025-06-18"

// mcpMaxMessageBytes bounds one inbound JSON-RPC line.
const mcpMaxMessageBytes = 8 << 20

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func logFilterProperties() map[string]any {
	return map[string]any{
		"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "description": "most recent matching lines to return"},
		"contains":  map[string]any{"type": "string", "description": "keep lines containing this text"},
		"level":     map[string]any{"type": "string", "enum": []string{"DEBUG", "INFO", "WARN", "ERROR"}},
		"requestId": map[string]any{"type": "string", "description": "the X-Beamfall-Request-Id of one request"},
	}
}

var mcpTools = func() []mcpTool {
	bundle := logFilterProperties()
	bundle["profiles"] = map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string", "enum": []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"}},
	}
	return []mcpTool{
		{Name: "catalog", Description: "List every Beamfall GraphQL field with its kind, arguments, result fields and the token tiers that may run it, plus a usage guide. Start here.", InputSchema: objectSchema(map[string]any{})},
		{Name: "graphql", Description: "Run one GraphQL query or mutation against /api/graphql with this server's token and return the data JSON.", InputSchema: objectSchema(map[string]any{
			"query":     map[string]any{"type": "string", "description": "GraphQL document, e.g. query { jobs { id status } }"},
			"variables": map[string]any{"type": "object", "description": "GraphQL variables"},
		}, "query")},
		{Name: "logs", Description: "Read recent redacted server log lines, optionally filtered.", InputSchema: objectSchema(logFilterProperties())},
		{Name: "bundle", Description: "Read the diagnostics bundle: version, uptime, runtime and memory statistics, filtered log lines and text runtime profiles.", InputSchema: objectSchema(bundle)},
		{Name: "health", Description: "Read the server's unauthenticated /health probe.", InputSchema: objectSchema(map[string]any{})},
	}
}()

const (
	mcpCatalogQuery = `query { agentCatalog { guide fields { name kind adminSession managerRole tokenTiers argumentsKnown arguments resultFields } } }`
	mcpLogsQuery    = `query L($limit: Int, $contains: String, $level: String, $requestId: String) { serverLogs(limit: $limit, contains: $contains, level: $level, requestId: $requestId) { available capacity lines } }`
	mcpBundleQuery  = `query B($profiles: [String!], $limit: Int, $contains: String, $level: String, $requestId: String) { diagnosticsBundle(profiles: $profiles, limit: $limit, contains: $contains, level: $level, requestId: $requestId) { version revision goVersion os arch startedAt uptimeSeconds goroutines cpus memory { heapAlloc heapInuse heapObjects stackInuse sys totalAlloc numGC pauseTotalNs } logs { available capacity lines } profiles { name text truncated } } }`
)

const mcpHelp = `Usage: beamfall mcp [--server URL] [--token TOKEN]

Run a Model Context Protocol server over stdio that proxies agent tool calls
(catalog, graphql, logs, bundle, health) to a running Beamfall server.
--server defaults to BEAMFALL_SERVER, --token to BEAMFALL_TOKEN. Mint a token
with: beamfall token create --name <name> --access cli|diagnostics|agent
`

func mcpCommand(ctx context.Context, args []string, stdout io.Writer) error {
	var opts cliHTTPOptions
	fs := commandFlagSet("beamfall mcp", stdout)
	addHTTPFlags(fs, &opts)
	fs.Usage = func() { _, _ = io.WriteString(fs.Output(), mcpHelp) }
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	resolved, err := resolveHTTPOptions(opts)
	if err != nil {
		return err
	}
	return serveMCP(ctx, resolved, os.Stdin, stdout)
}

// serveMCP answers newline-delimited JSON-RPC requests from in until EOF or
// until ctx is cancelled. Lines are read on their own goroutine so a client
// that holds stdin open cannot keep the server alive past shutdown. On return
// the reader is released: in is closed when it is an io.Closer, which unblocks
// a pending read.
func serveMCP(ctx context.Context, opts cliHTTPOptions, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if closer, ok := in.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}
	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 64<<10), mcpMaxMessageBytes)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		readErr <- scanner.Err()
	}()
	encoder := json.NewEncoder(out)
	for {
		var line string
		select {
		case <-ctx.Done():
			return nil // the deferred cancel and Close release the reader
		case err := <-readErr:
			if err != nil {
				return fmt.Errorf("mcp: read stdin: %w", err)
			}
			return nil
		case line = <-lines:
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			if err := encoder.Encode(mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}
		result, rpcErr := handleMCP(ctx, opts, req)
		if len(req.ID) == 0 {
			continue // a notification gets no reply
		}
		resp := mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		if err := encoder.Encode(resp); err != nil {
			return err
		}
	}
}

func handleMCP(ctx context.Context, opts cliHTTPOptions, req mcpRequest) (any, *mcpError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = mcpProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "beamfall", "version": capabilities.Current().Version},
			"instructions":    "Call the catalog tool first: it lists every Beamfall GraphQL field and the token tiers that may run it.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid tools/call params"}
		}
		text, err := callMCPTool(ctx, opts, params.Name, params.Arguments)
		if errors.Is(err, errUnknownMCPTool) {
			return nil, &mcpError{Code: -32602, Message: err.Error()}
		}
		if err != nil {
			return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}, nil
		}
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}}, nil
	default:
		if strings.HasPrefix(req.Method, "notifications/") {
			return nil, nil
		}
		return nil, &mcpError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

var errUnknownMCPTool = errors.New("unknown tool")

func callMCPTool(ctx context.Context, opts cliHTTPOptions, name string, args map[string]any) (string, error) {
	switch name {
	case "catalog":
		return mcpGraphQL(ctx, opts, mcpCatalogQuery, nil)
	case "graphql":
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return "", errors.New("query is required")
		}
		variables, _ := args["variables"].(map[string]any)
		return mcpGraphQL(ctx, opts, query, variables)
	case "logs":
		return mcpGraphQL(ctx, opts, mcpLogsQuery, pickArgs(args, "limit", "contains", "level", "requestId"))
	case "bundle":
		return mcpGraphQL(ctx, opts, mcpBundleQuery, pickArgs(args, "profiles", "limit", "contains", "level", "requestId"))
	case "health":
		return mcpHealth(ctx, opts)
	default:
		return "", fmt.Errorf("%w %q", errUnknownMCPTool, name)
	}
}

func pickArgs(args map[string]any, names ...string) map[string]any {
	out := map[string]any{}
	for _, name := range names {
		if value, ok := args[name]; ok && value != nil {
			out[name] = value
		}
	}
	return out
}

func mcpGraphQL(ctx context.Context, opts cliHTTPOptions, query string, variables map[string]any) (string, error) {
	var data json.RawMessage
	if err := cliGraphQL(ctx, opts, query, variables, &data); err != nil {
		return "", err
	}
	return string(data), nil
}

func mcpHealth(ctx context.Context, opts cliHTTPOptions) (string, error) {
	endpoint := strings.TrimRight(opts.server, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := defaultCLIHTTPClient.Do(req)
	if err != nil {
		return "", errors.New("could not reach Beamfall server")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n%s", resp.Status, strings.TrimSpace(string(body))), nil
}

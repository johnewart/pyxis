/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package mcpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/johnewart/pyxis/internal/tracing"
	"github.com/johnewart/pyxis/llmdlog"
)

// UpstreamTool is one entry of an upstream server's tools/list manifest.
type UpstreamTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// UpstreamResource is one entry of resources/list.
type UpstreamResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// PromptArgument describes one argument of an upstream prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// UpstreamPrompt is one entry of prompts/list.
type UpstreamPrompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// ContentBlock is a single block of an upstream tools/call result.
type ContentBlock struct {
	Type string         `json:"type"`
	Text string         `json:"text,omitempty"`
	Raw  map[string]any `json:"-"`
}

// Transport is one connected upstream MCP server.
type Transport interface {
	Connect(ctx context.Context) error
	ListTools(ctx context.Context) ([]UpstreamTool, error)
	ListResources(ctx context.Context) ([]UpstreamResource, error)
	ListPrompts(ctx context.Context) ([]UpstreamPrompt, error)
	CallTool(ctx context.Context, name string, args map[string]any) ([]ContentBlock, error)
	Close() error
}

// ---------------------------------------------------------------------------
// JSON-RPC plumbing
// ---------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	protocolVersion = "2024-11-05"
	clientName      = "llmd-mcp-proxy"
	clientVersion   = "1.0.0"
)

func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": clientName, "version": clientVersion},
	}
}

type listToolsResult struct {
	Tools      []UpstreamTool `json:"tools"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type listResourcesResult struct {
	Resources  []UpstreamResource `json:"resources"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

type listPromptsResult struct {
	Prompts    []UpstreamPrompt `json:"prompts"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type callToolResult struct {
	Content []json.RawMessage `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// caller is whatever can issue a single JSON-RPC call (stdio pipe or HTTP post).
type caller interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params any) error
}

func normalizeInputSchema(raw map[string]any) map[string]any {
	if len(raw) > 0 {
		return raw
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func listTools(ctx context.Context, c caller) ([]UpstreamTool, error) {
	var out []UpstreamTool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page listToolsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		for _, t := range page.Tools {
			name := strings.TrimSpace(t.Name)
			if name == "" {
				continue
			}
			out = append(out, UpstreamTool{
				Name:        name,
				Description: t.Description,
				InputSchema: normalizeInputSchema(t.InputSchema),
			})
		}
		cursor = page.NextCursor
		if cursor == "" {
			return out, nil
		}
	}
}

// listResources returns empty when the upstream does not implement resources.
func listResources(ctx context.Context, c caller) ([]UpstreamResource, error) {
	var out []UpstreamResource
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "resources/list", params)
		if err != nil {
			return out, nil
		}
		var page listResourcesResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return out, nil
		}
		for _, r := range page.Resources {
			if strings.TrimSpace(r.URI) == "" {
				continue
			}
			r.URI = strings.TrimSpace(r.URI)
			out = append(out, r)
		}
		cursor = page.NextCursor
		if cursor == "" {
			return out, nil
		}
	}
}

// listPrompts returns empty when the upstream does not implement prompts.
func listPrompts(ctx context.Context, c caller) ([]UpstreamPrompt, error) {
	var out []UpstreamPrompt
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "prompts/list", params)
		if err != nil {
			return out, nil
		}
		var page listPromptsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return out, nil
		}
		for _, p := range page.Prompts {
			if strings.TrimSpace(p.Name) == "" {
				continue
			}
			p.Name = strings.TrimSpace(p.Name)
			out = append(out, p)
		}
		cursor = page.NextCursor
		if cursor == "" {
			return out, nil
		}
	}
}

func callTool(ctx context.Context, c caller, name string, args map[string]any) ([]ContentBlock, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var res callToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}
	blocks := make([]ContentBlock, 0, len(res.Content))
	for _, item := range res.Content {
		var m map[string]any
		if err := json.Unmarshal(item, &m); err != nil {
			continue
		}
		b := ContentBlock{Raw: m}
		if t, ok := m["type"].(string); ok {
			b.Type = t
		}
		if txt, ok := m["text"].(string); ok {
			b.Text = txt
		}
		blocks = append(blocks, b)
	}
	if res.IsError {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		msg := strings.Join(parts, "\n")
		if msg == "" {
			msg = fmt.Sprintf("upstream MCP tool %s returned isError", name)
		}
		return nil, errors.New(msg)
	}
	return blocks, nil
}

// MapContentToText flattens content blocks the way mapMcpContentToText.ts does.
func MapContentToText(blocks []ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
			continue
		}
		if raw, err := json.Marshal(b.Raw); err == nil {
			parts = append(parts, string(raw))
		}
	}
	return strings.Join(parts, "\n")
}

// ---------------------------------------------------------------------------
// stdio transport
// ---------------------------------------------------------------------------

// StdioTransportOptions configures a subprocess-hosted upstream MCP server.
// Env carries already-resolved values: refs are resolved by the caller at the
// moment of spawn and never stored.
type StdioTransportOptions struct {
	ProviderID    string
	Command       string
	Args          []string
	Env           []string
	Dir           string
	CallTimeoutMs int
}

type stdioTransport struct {
	opts StdioTransportOptions

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *stderrTail // current connection's stderr; nil when not connected
	nextID  int64
	pending map[int64]chan rpcResponse
	closed  bool
}

// NewStdioTransport builds an unconnected stdio transport.
func NewStdioTransport(opts StdioTransportOptions) Transport {
	return &stdioTransport{opts: opts, pending: map[int64]chan rpcResponse{}}
}

func (t *stdioTransport) Connect(ctx context.Context) error {
	_ = t.Close()
	t.mu.Lock()
	cmd := exec.Command(t.opts.Command, t.opts.Args...)
	cmd.Env = t.opts.Env
	if dir := strings.TrimSpace(t.opts.Dir); dir != "" {
		// Callers must hand us an already-contained directory: subprocess working
		// directories are not covered by the workspace *os.Root.
		cmd.Dir = dir
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.mu.Unlock()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.mu.Unlock()
		return err
	}
	// Keep the tail of stderr: it is the only explanation when the server dies.
	tail := &stderrTail{}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		t.mu.Unlock()
		return err
	}
	t.cmd, t.stdin, t.stdout, t.stderr = cmd, stdin, bufio.NewReaderSize(stdout, 1<<20), tail
	t.closed = false
	t.pending = map[int64]chan rpcResponse{}
	reader := t.stdout
	t.mu.Unlock()

	go t.readLoop(reader, tail)

	if _, err := t.call(ctx, "initialize", initializeParams()); err != nil {
		_ = t.Close()
		return fmt.Errorf("mcp stdio initialize (%s): %w", t.opts.ProviderID, err)
	}
	if err := t.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		_ = t.Close()
		return err
	}
	return nil
}

func (t *stdioTransport) readLoop(r *bufio.Reader, tail *stderrTail) {
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var resp rpcResponse
			if json.Unmarshal(bytes.TrimSpace(line), &resp) == nil && resp.ID != nil {
				t.mu.Lock()
				ch := t.pending[*resp.ID]
				delete(t.pending, *resp.ID)
				t.mu.Unlock()
				if ch != nil {
					ch <- resp
				}
			}
		}
		if err != nil {
			t.mu.Lock()
			// Our own Close (or a reconnect replacing this connection) ends the
			// loop on purpose; anything else means the server went away.
			unexpected := !t.closed && t.stderr == tail
			for id, ch := range t.pending {
				close(ch)
				delete(t.pending, id)
			}
			t.mu.Unlock()
			if unexpected {
				llmdlog.Stdout("mcpproxy", llmdlog.LevelWarn,
					"MCP server %s (stdio) stopped responding: %v; stderr tail: %q",
					t.opts.ProviderID, err, tail.String())
			}
			return
		}
	}
}

func (t *stdioTransport) writeJSON(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stdin == nil {
		return fmt.Errorf("MCP transport for %s is not connected", t.opts.ProviderID)
	}
	if _, err := t.stdin.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func (t *stdioTransport) notify(ctx context.Context, method string, params any) error {
	return t.writeJSON(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	if t.stdin == nil {
		t.mu.Unlock()
		return nil, fmt.Errorf("MCP transport for %s is not connected", t.opts.ProviderID)
	}
	t.nextID++
	id := t.nextID
	ch := make(chan rpcResponse, 1)
	t.pending[id] = ch
	t.mu.Unlock()

	if err := t.writeJSON(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, err
	}

	ctx, cancel := t.withCallTimeout(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			t.mu.Lock()
			tail := t.stderr
			t.mu.Unlock()
			if s := tail.String(); s != "" {
				return nil, fmt.Errorf("MCP server %s closed the connection: %s", t.opts.ProviderID, s)
			}
			return nil, fmt.Errorf("MCP server %s closed the connection", t.opts.ProviderID)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (t *stdioTransport) withCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	ms := t.opts.CallTimeoutMs
	if ms <= 0 {
		ms = DefaultCallTimeoutMs
	}
	return context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
}

func (t *stdioTransport) ListTools(ctx context.Context) ([]UpstreamTool, error) {
	return listTools(ctx, t)
}
func (t *stdioTransport) ListResources(ctx context.Context) ([]UpstreamResource, error) {
	return listResources(ctx, t)
}
func (t *stdioTransport) ListPrompts(ctx context.Context) ([]UpstreamPrompt, error) {
	return listPrompts(ctx, t)
}
func (t *stdioTransport) CallTool(ctx context.Context, name string, args map[string]any) ([]ContentBlock, error) {
	return callTool(ctx, t, name, args)
}

func (t *stdioTransport) Close() error {
	t.mu.Lock()
	cmd, stdin := t.cmd, t.stdin
	t.cmd, t.stdin, t.stdout = nil, nil, nil
	t.closed = true
	t.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP transport
// ---------------------------------------------------------------------------

// HTTPTransportOptions configures a streamable-HTTP upstream MCP server.
// Headers carry already-resolved values.
type HTTPTransportOptions struct {
	ProviderID    string
	URL           string
	Headers       map[string]string
	CallTimeoutMs int
	Client        *http.Client
}

type httpTransport struct {
	opts   HTTPTransportOptions
	mu     sync.Mutex
	nextID int64
	// sessionID is the Mcp-Session-Id the server assigned at initialize, if any.
	sessionID string
}

// NewHTTPTransport builds an unconnected streamable-HTTP transport.
func NewHTTPTransport(opts HTTPTransportOptions) Transport {
	return &httpTransport{opts: opts}
}

func (t *httpTransport) client() *http.Client {
	if t.opts.Client != nil {
		return t.opts.Client
	}
	ms := t.opts.CallTimeoutMs
	if ms <= 0 {
		ms = DefaultCallTimeoutMs
	}
	return &http.Client{
		Timeout:   time.Duration(ms) * time.Millisecond,
		Transport: tracing.HTTPTransport(nil, nil),
	}
}

func (t *httpTransport) Connect(ctx context.Context) error {
	if _, err := t.call(ctx, "initialize", initializeParams()); err != nil {
		return fmt.Errorf("mcp http initialize (%s): %w", t.opts.ProviderID, err)
	}
	return t.notify(ctx, "notifications/initialized", map[string]any{})
}

func (t *httpTransport) post(ctx context.Context, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.opts.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.opts.Headers {
		req.Header.Set(k, v)
	}
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	return t.client().Do(req)
}

func (t *httpTransport) notify(ctx context.Context, method string, params any) error {
	resp, err := t.post(ctx, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (t *httpTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()

	resp, err := t.post(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp http %s: status %d", method, resp.StatusCode)
	}
	body := raw
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		body = extractSSEData(raw)
	}
	var rpcResp rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(body), &rpcResp); err != nil {
		return nil, fmt.Errorf("mcp http %s: %w", method, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp %s: %s", method, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// extractSSEData returns the last `data:` payload of an SSE response body.
func extractSSEData(raw []byte) []byte {
	var last []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, []byte("data:")) {
			last = bytes.TrimSpace(line[len("data:"):])
		}
	}
	if last == nil {
		return raw
	}
	return last
}

func (t *httpTransport) ListTools(ctx context.Context) ([]UpstreamTool, error) {
	return listTools(ctx, t)
}
func (t *httpTransport) ListResources(ctx context.Context) ([]UpstreamResource, error) {
	return listResources(ctx, t)
}
func (t *httpTransport) ListPrompts(ctx context.Context) ([]UpstreamPrompt, error) {
	return listPrompts(ctx, t)
}
func (t *httpTransport) CallTool(ctx context.Context, name string, args map[string]any) ([]ContentBlock, error) {
	return callTool(ctx, t, name, args)
}
func (t *httpTransport) Close() error { return nil }

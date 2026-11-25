package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/backend"
	openaillm "github.com/johnewart/pyxis/internal/llm/openai"
)

// DefaultBaseURL is the OpenRouter OpenAI-compatible endpoint.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

var _ backend.Backend = (*Client)(nil)

// Client wraps the OpenAI-compatible client, adding the `provider` request
// object and reporting the serving upstream as AssistantReply.ServedBy.
type Client struct {
	inner    *openaillm.Client
	defaults *llmv1.OpenRouterCallOptions
	health   EndpointHealthOptions
	prober   *healthProber
}

// New builds a client. defaults are the provider-level routing options.
func New(apiKey, baseURL, model string, maxTokens int, defaults *llmv1.OpenRouterCallOptions) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	c := &Client{
		defaults: defaults,
		health:   ResolveEndpointHealth(nil, 0),
	}
	hc := &http.Client{Transport: &transport{base: http.DefaultTransport, client: c}}
	c.prober = newHealthProber(apiKey, baseURL, hc)
	c.inner = openaillm.NewWithHTTPClient(apiKey, baseURL, model, maxTokens, hc)
	return c
}

type ctxKey struct{}

// callState carries per-call routing options in and the served provider out.
type callState struct {
	opts     *llmv1.OpenRouterCallOptions
	provider map[string]any
	mu       sync.Mutex
	served   string
}

func (s *callState) setServed(v string) {
	if v == "" {
		return
	}
	s.mu.Lock()
	s.served = v
	s.mu.Unlock()
}

func (s *callState) getServed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served
}

func (c *Client) withState(ctx context.Context, opt *llmv1.CallOptions) (context.Context, *callState) {
	merged := MergeOptions(c.defaults, opt.GetOpenrouter())
	st := &callState{opts: merged, provider: ProviderObject(merged)}
	return context.WithValue(ctx, ctxKey{}, st), st
}

// Chat implements backend.Backend.
func (c *Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	ctx, st := c.withState(ctx, opt)
	r, err := c.inner.Chat(ctx, messages, opt, tools)
	if err != nil {
		return nil, err
	}
	r.ServedBy = st.getServed()
	return r, nil
}

// Generate implements backend.Backend.
func (c *Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	ctx, st := c.withState(ctx, opt)
	r, err := c.inner.Generate(ctx, systemPrompt, prompt, opt, tools)
	if err != nil {
		return nil, err
	}
	r.ServedBy = st.getServed()
	return r, nil
}

// ChatStream implements backend.Backend; ServedBy is set on the final reply frame.
func (c *Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("openrouter: emit callback is required")
	}
	ctx, st := c.withState(ctx, opt)
	return c.inner.ChatStream(ctx, messages, opt, tools, func(f *llmv1.ConversationStreamFrame) error {
		if sc := f.GetStreamComplete(); sc != nil {
			sc.ServedBy = st.getServed()
		}
		return emit(f)
	})
}

// transport injects `provider` into chat request bodies and captures the
// response `provider` field (JSON body or SSE chunks).
type transport struct {
	base   http.RoundTripper
	client *Client
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	st, _ := req.Context().Value(ctxKey{}).(*callState)
	if st == nil {
		return t.base.RoundTrip(req)
	}
	if req.Body != nil && (st.provider != nil || (t.client != nil && t.client.health.Enabled)) {
		raw, err := io.ReadAll(req.Body)
		if cerr := req.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			return nil, fmt.Errorf("openrouter: read request body: %w", err)
		}
		body := raw
		if provider := t.providerWithHealth(req.Context(), raw, st); provider != nil {
			body, err = injectProvider(raw, provider)
			if err != nil {
				return nil, err
			}
		}
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &sseSniffer{rc: resp.Body, st: st}
		return resp, nil
	}
	raw, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("openrouter: read response body: %w", err)
	}
	st.setServed(providerFromJSON(raw))
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

// providerWithHealth returns the provider object with deranked upstreams merged into
// ignore (names in only are never ignored). A probe failure is logged and the call
// proceeds with the unmodified provider object.
func (t *transport) providerWithHealth(ctx context.Context, raw []byte, st *callState) map[string]any {
	if t.client == nil || !t.client.health.Enabled {
		return st.provider
	}
	var b struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		slog.Warn("openrouter: endpoint health skipped: cannot read model from request body", "error", err)
		return st.provider
	}
	deranked, err := t.client.DerankedProviders(ctx, b.Model)
	if err != nil {
		slog.Warn("openrouter: endpoint health probe failed; proceeding without deranked ignore", "model", b.Model, "error", err)
		return st.provider
	}
	if len(deranked) == 0 {
		return st.provider
	}
	return ProviderObject(MergeOptions(st.opts, &llmv1.OpenRouterCallOptions{Ignore: deranked}))
}

func injectProvider(raw []byte, provider map[string]any) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("openrouter: decode request body: %w", err)
	}
	pb, err := json.Marshal(provider)
	if err != nil {
		return nil, fmt.Errorf("openrouter: encode provider object: %w", err)
	}
	m["provider"] = pb
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("openrouter: encode request body: %w", err)
	}
	return out, nil
}

// providerFromJSON returns the top-level string "provider" field, or ""
// (error bodies and non-JSON payloads legitimately have none).
func providerFromJSON(raw []byte) string {
	var v struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v.Provider
}

// sseSniffer passes bytes through while scanning "data:" lines for a provider.
type sseSniffer struct {
	rc  io.ReadCloser
	st  *callState
	buf []byte
}

func (s *sseSniffer) Read(p []byte) (int, error) {
	n, err := s.rc.Read(p)
	s.buf = append(s.buf, p[:n]...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(s.buf[:i]))
		s.buf = s.buf[i+1:]
		if d, ok := strings.CutPrefix(line, "data:"); ok {
			s.st.setServed(providerFromJSON([]byte(strings.TrimSpace(d))))
		}
	}
	return n, err
}

func (s *sseSniffer) Close() error { return s.rc.Close() }

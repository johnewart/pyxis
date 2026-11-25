package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// healthProber fetches OpenRouter's per-model endpoint list and caches the deranked
// upstream names per model for the TTL. The clock and base URL are injectable.
type healthProber struct {
	apiKey  string
	baseURL string
	hc      *http.Client
	now     func() time.Time

	mu       sync.Mutex
	cache    map[string]healthEntry
	inflight map[string]*probeCall
}

type healthEntry struct {
	deranked  []string
	fetchedAt time.Time
	expires   time.Time
}

// probeCall is one in-flight probe shared by all concurrent callers for a model.
type probeCall struct {
	done  chan struct{}
	entry healthEntry
	err   error
}

// probeTimeout bounds a single endpoint-health request.
const probeTimeout = 5 * time.Second

func newHealthProber(apiKey, baseURL string, hc *http.Client) *healthProber {
	return &healthProber{
		apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), hc: hc, now: time.Now,
		cache: map[string]healthEntry{}, inflight: map[string]*probeCall{},
	}
}

// deranked returns upstream names reported unhealthy (status < 0) for model. Fresh
// cache entries are reused; failures are returned and not cached.
func (p *healthProber) deranked(ctx context.Context, model string, ttl time.Duration) ([]string, error) {
	e, err := p.entry(ctx, model, ttl)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), e.deranked...), nil
}

// entry returns the fresh cache entry for model, probing at most once at a time per model.
func (p *healthProber) entry(ctx context.Context, model string, ttl time.Duration) (healthEntry, error) {
	p.mu.Lock()
	if e, ok := p.cache[model]; ok && p.now().Before(e.expires) {
		p.mu.Unlock()
		return e, nil
	}
	if call, ok := p.inflight[model]; ok {
		p.mu.Unlock()
		select {
		case <-call.done:
			return call.entry, call.err
		case <-ctx.Done():
			return healthEntry{}, fmt.Errorf("openrouter: waiting for health probe of %q: %w", model, ctx.Err())
		}
	}
	call := &probeCall{done: make(chan struct{})}
	p.inflight[model] = call
	p.mu.Unlock()

	// The probe is shared, so it must not be canceled by one caller's context.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeTimeout)
	names, err := p.probe(pctx, model)
	cancel()

	p.mu.Lock()
	if err == nil {
		now := p.now()
		call.entry = healthEntry{deranked: names, fetchedAt: now, expires: now.Add(ttl)}
		p.cache[model] = call.entry
	}
	call.err = err
	delete(p.inflight, model)
	p.mu.Unlock()
	close(call.done)
	return call.entry, call.err
}

func (p *healthProber) probe(ctx context.Context, model string) ([]string, error) {
	// Model ids are "author/slug"; escape each segment but keep the slash.
	parts := strings.Split(model, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	u := p.baseURL + "/models/" + strings.Join(parts, "/") + "/endpoints"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter: build health request: %w", err)
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	// Mark the context so the provider transport passes this request through untouched.
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, (*callState)(nil)))
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: health request for %q: %w", model, err)
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("openrouter: read health response for %q: %w", model, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter: health request for %q: status %d", model, resp.StatusCode)
	}
	var v struct {
		Data struct {
			Endpoints []struct {
				ProviderName string `json:"provider_name"`
				Status       *int   `json:"status"`
			} `json:"endpoints"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("openrouter: decode health response for %q: %w", model, err)
	}
	var out []string
	for _, e := range v.Data.Endpoints {
		if e.Status != nil && *e.Status < 0 && e.ProviderName != "" {
			out = append(out, e.ProviderName)
		}
	}
	return out, nil
}

// DefaultEndpointHealthTTL is how long an endpoint-health result is cached when the
// provider config does not set a TTL.
const DefaultEndpointHealthTTL = 60 * time.Second

// EndpointHealthOptions controls avoidance of upstream endpoints reported unhealthy.
type EndpointHealthOptions struct {
	// Enabled turns endpoint-health avoidance on.
	Enabled bool
	// TTL is how long a health result is cached. Always positive after resolution.
	TTL time.Duration
}

// ResolveEndpointHealth applies defaults to stored config values: enabled when enabled is
// nil, and a 60s TTL when ttlSeconds is not positive.
func ResolveEndpointHealth(enabled *bool, ttlSeconds int32) EndpointHealthOptions {
	o := EndpointHealthOptions{Enabled: true, TTL: DefaultEndpointHealthTTL}
	if enabled != nil {
		o.Enabled = *enabled
	}
	if ttlSeconds > 0 {
		o.TTL = time.Duration(ttlSeconds) * time.Second
	}
	return o
}

// HealthOptions returns the client's endpoint-health options.
func (c *Client) HealthOptions() EndpointHealthOptions { return c.health }

// WithClock sets the clock used for health-cache expiry (for tests) and returns the client.
func (c *Client) WithClock(now func() time.Time) *Client {
	if now != nil {
		c.prober.mu.Lock()
		c.prober.now = now
		c.prober.mu.Unlock()
	}
	return c
}

// EndpointHealthStatus is the deranked upstream set for a model and when it was fetched.
type EndpointHealthStatus struct {
	Model     string
	Deranked  []string
	FetchedAt time.Time
}

// EndpointHealth returns the deranked upstream names for model and the time they were
// fetched, using the per-model TTL cache. With health avoidance disabled it returns an
// empty status and makes no probe.
func (c *Client) EndpointHealth(ctx context.Context, model string) (EndpointHealthStatus, error) {
	st := EndpointHealthStatus{Model: model}
	if !c.health.Enabled || c.prober == nil || model == "" {
		return st, nil
	}
	e, err := c.prober.entry(ctx, model, c.health.TTL)
	if err != nil {
		return st, err
	}
	st.Deranked = append([]string(nil), e.deranked...)
	st.FetchedAt = e.fetchedAt
	return st, nil
}

// DerankedProviders returns the upstream names currently deranked for model, using the
// per-model TTL cache. It returns nil without any probe when health avoidance is disabled.
func (c *Client) DerankedProviders(ctx context.Context, model string) ([]string, error) {
	if !c.health.Enabled || c.prober == nil || model == "" {
		return nil, nil
	}
	return c.prober.deranked(ctx, model, c.health.TTL)
}

// WithEndpointHealth sets the endpoint-health options and returns the client.
func (c *Client) WithEndpointHealth(o EndpointHealthOptions) *Client {
	c.health = o
	return c
}

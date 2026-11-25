/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// Health polling for the managed llama-server. Semantics: the /v1 suffix is
// stripped before /health and /models, a process that exits during startup is
// surfaced as an error carrying the bounded stderr tail, and an absent model
// row is "unknown" (keep polling) rather than loaded.
//
// Every timeout is injectable so tests run in milliseconds instead of sleeping.

package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Ported timing constants. Defaults match the TypeScript original.
const (
	ServerReadyPollInterval = 300 * time.Millisecond
	ServerReadyMaxWait      = 120 * time.Second
	ModelLoadPollInterval   = 500 * time.Millisecond
	ModelLoadMaxWait        = 600 * time.Second

	// stderrTailLimit bounds the retained stderr so a chatty server cannot
	// grow unbounded state (per-event work stays O(delta)).
	stderrTailLimit = 8 * 1024

	// httpProbeTimeout bounds a single /health or /models request.
	httpProbeTimeout = 5 * time.Second
)

// tailBuffer keeps only the last n bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func newTailBuffer(limit int) *tailBuffer {
	if limit <= 0 {
		limit = stderrTailLimit
	}
	return &tailBuffer{limit: limit}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.limit:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// ProcessProbe is the subset of a managed child the health waits inspect. The
// registry's managedChild satisfies it; tests can substitute a fake.
type ProcessProbe interface {
	// ExitCode is nil while the process is still running.
	ExitCode() *int
	// StderrTail is the bounded tail of the process's stderr.
	StderrTail() string
}

// WaitOptions tunes the readiness waits. Zero values fall back to the ported
// defaults.
type WaitOptions struct {
	// Process, when set, aborts the wait as soon as the child exits.
	Process ProcessProbe
	// HTTPClient performs the probe requests.
	HTTPClient *http.Client
	// PollInterval and MaxWait override the ported defaults.
	PollInterval time.Duration
	MaxWait      time.Duration
	// OnTick fires before each probe with the elapsed time.
	OnTick func(elapsed time.Duration)
}

func (o WaitOptions) client() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: httpProbeTimeout}
}

func (o WaitOptions) poll(def time.Duration) time.Duration {
	if o.PollInterval > 0 {
		return o.PollInterval
	}
	return def
}

func (o WaitOptions) max(def time.Duration) time.Duration {
	if o.MaxWait > 0 {
		return o.MaxWait
	}
	return def
}

// rootURL strips a trailing /v1 (or /v1/) from an OpenAI-style base URL.
func rootURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(trimmed, "/v1") {
		trimmed = strings.TrimSuffix(trimmed, "/v1")
	}
	return strings.TrimRight(trimmed, "/")
}

// ProcessStartupError reports a child that died (or was killed) during startup,
// including the stderr tail. It returns nil while the process is healthy.
// Port of processStartupError.
func ProcessStartupError(proc ProcessProbe) error {
	if proc == nil {
		return nil
	}
	code := proc.ExitCode()
	if code == nil {
		return nil
	}
	detail := strings.TrimSpace(proc.StderrTail())
	if detail != "" {
		return fmt.Errorf("llama-server exited during startup (code %d): %s", *code, detail)
	}
	return fmt.Errorf("llama-server exited during startup (code %d)", *code)
}

// WaitForServerReady polls <root>/health until the server answers 2xx, the
// process exits, the context is cancelled, or MaxWait elapses.
// Port of waitForLlamaServerReady.
func WaitForServerReady(ctx context.Context, baseURL string, opts WaitOptions) error {
	healthURL := rootURL(baseURL) + "/health"
	client := opts.client()
	poll := opts.poll(ServerReadyPollInterval)
	deadline := time.Now().Add(opts.max(ServerReadyMaxWait))
	start := time.Now()

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ProcessStartupError(opts.Process); err != nil {
			return err
		}
		if opts.OnTick != nil {
			opts.OnTick(time.Since(start))
		}
		if probeOK(ctx, client, healthURL) {
			return nil
		}
		if err := sleepCtx(ctx, poll); err != nil {
			return err
		}
	}

	tail := ""
	if opts.Process != nil {
		tail = strings.TrimSpace(opts.Process.StderrTail())
	}
	if tail != "" {
		return fmt.Errorf("llama-server did not become ready in time. Last output: %s", tail)
	}
	return fmt.Errorf("llama-server did not become ready in time")
}

func probeOK(ctx context.Context, client *http.Client, url string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, httpProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()
	return res.StatusCode >= 200 && res.StatusCode < 300
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ModelLoadState is a router-reported model state.
type ModelLoadState string

// The router status vocabulary. Unknown is the pessimistic state: an absent row
// must never read as loaded.
const (
	ModelUnloaded ModelLoadState = "unloaded"
	ModelLoading  ModelLoadState = "loading"
	ModelLoaded   ModelLoadState = "loaded"
	ModelSleeping ModelLoadState = "sleeping"
	ModelFailed   ModelLoadState = "failed"
	ModelUnknown  ModelLoadState = "unknown"
)

// ModelEntry is one row of the router's GET /models payload.
type ModelEntry struct {
	ID       string
	Path     string
	Status   ModelLoadState
	Failed   bool
	ExitCode *int
}

type modelsPayload struct {
	Data []struct {
		ID     string `json:"id"`
		Path   string `json:"path"`
		Status *struct {
			Value     string `json:"value"`
			Failed    bool   `json:"failed"`
			ExitCode  *int   `json:"exit_code"`
			ExitCode2 *int   `json:"exitCode"`
		} `json:"status"`
	} `json:"data"`
}

// ParseModelsPayload parses a router-mode GET /models body.
// Port of parseLlamaServerModelsPayload.
func ParseModelsPayload(raw []byte) []ModelEntry {
	var payload modelsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	entries := make([]ModelEntry, 0, len(payload.Data))
	for _, row := range payload.Data {
		entry := ModelEntry{ID: row.ID, Path: row.Path, Status: ModelUnknown}
		value := ""
		if row.Status != nil {
			value = row.Status.Value
			entry.Failed = row.Status.Failed
			if row.Status.ExitCode != nil {
				entry.ExitCode = row.Status.ExitCode
			} else if row.Status.ExitCode2 != nil {
				entry.ExitCode = row.Status.ExitCode2
			}
		}
		switch {
		case entry.Failed:
			entry.Status = ModelFailed
		case value == string(ModelUnloaded), value == string(ModelLoading),
			value == string(ModelLoaded), value == string(ModelSleeping):
			entry.Status = ModelLoadState(value)
		}
		if entry.ID != "" || entry.Path != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// normalizeModelKey lowercases a model name, dropping any directory prefix and
// a trailing .gguf extension.
func normalizeModelKey(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	base := trimmed
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(base)
	return strings.TrimSuffix(base, ".gguf")
}

// ModelEntryMatchesCandidate reports whether entry names any of candidates,
// matching on id, on the .gguf-stripped stem, or on the path (case-insensitive).
// Port of modelEntryMatchesCandidate.
func ModelEntryMatchesCandidate(entry ModelEntry, candidates []string) bool {
	keys := map[string]struct{}{}
	for _, candidate := range candidates {
		if normalized := normalizeModelKey(candidate); normalized != "" {
			keys[normalized] = struct{}{}
		}
		if trimmed := strings.ToLower(strings.TrimSpace(candidate)); trimmed != "" {
			keys[trimmed] = struct{}{}
		}
	}
	entryKeys := []string{
		normalizeModelKey(entry.ID),
		normalizeModelKey(entry.Path),
		strings.ToLower(strings.TrimSpace(entry.ID)),
	}
	for _, key := range entryKeys {
		if key == "" {
			continue
		}
		if _, ok := keys[key]; ok {
			return true
		}
	}
	return false
}

// FetchModels reads the router's model rows from <root>/models, falling back to
// <root>/v1/models. A server that cannot answer yields no rows, not an error —
// the caller keeps polling. Port of fetchLlamaServerModels.
func FetchModels(ctx context.Context, baseURL string, client *http.Client) ([]ModelEntry, error) {
	if client == nil {
		client = &http.Client{Timeout: httpProbeTimeout}
	}
	root := rootURL(baseURL)
	for _, url := range []string{root + "/models", root + "/v1/models"} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, ok := fetchBody(ctx, client, url)
		if !ok {
			continue
		}
		return ParseModelsPayload(body), nil
	}
	return nil, nil
}

func fetchBody(ctx context.Context, client *http.Client, url string) ([]byte, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, httpProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, false
	}
	return body, true
}

// ModelWaitOptions tunes WaitForModelLoaded. OnModelTick reports the observed
// state on every poll.
type ModelWaitOptions struct {
	WaitOptions
	OnModelTick func(elapsed time.Duration, state ModelLoadState)
}

// WaitForModelLoaded polls GET /models until one of modelCandidates reports
// loaded (or sleeping — a loaded model idled out is still loaded).
// Port of waitForLlamaModelLoaded.
func WaitForModelLoaded(ctx context.Context, baseURL string, modelCandidates []string, opts ModelWaitOptions) error {
	client := opts.client()
	poll := opts.poll(ModelLoadPollInterval)
	deadline := time.Now().Add(opts.max(ModelLoadMaxWait))
	start := time.Now()

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ProcessStartupError(opts.Process); err != nil {
			return err
		}

		models, err := FetchModels(ctx, baseURL, client)
		if err != nil {
			return err
		}
		state := ModelUnknown
		var match *ModelEntry
		for i := range models {
			if ModelEntryMatchesCandidate(models[i], modelCandidates) {
				match = &models[i]
				state = models[i].Status
				break
			}
		}
		if opts.OnModelTick != nil {
			opts.OnModelTick(time.Since(start), state)
		}

		if state == ModelLoaded || state == ModelSleeping {
			return nil
		}
		if state == ModelFailed || (match != nil && match.Failed) {
			if match != nil && match.ExitCode != nil {
				return fmt.Errorf("llama-server failed to load model (exit %d)", *match.ExitCode)
			}
			return fmt.Errorf("llama-server failed to load model")
		}

		if err := sleepCtx(ctx, poll); err != nil {
			return err
		}
	}
	return fmt.Errorf("model did not finish loading in time")
}

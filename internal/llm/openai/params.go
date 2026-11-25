package openaillm

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/sashabaranov/go-openai"
)

// Request parameters this client knows how to adjust when a model rejects them.
const (
	paramMaxTokens           = "max_tokens"
	paramMaxCompletionTokens = "max_completion_tokens"
	paramTemperature         = "temperature"
	paramTopP                = "top_p"
	paramStreamOptions       = "stream_options"
)

// maxParamRetries bounds how many times one call re-sends a request after the
// server rejects a parameter. Each retry must learn something new, so this is a
// ceiling, not the usual count (which is 0 or 1).
const maxParamRetries = 3

// maxLearnedModels bounds the per-client memory of model quirks. A client talks
// to a handful of models; past this the map is cleared and relearned.
const maxLearnedModels = 256

// isOfficialOpenAI reports whether baseURL is OpenAI's own API (empty means the
// go-openai default, which is api.openai.com). Only the official API is known to
// accept max_completion_tokens on every chat model; compatible servers
// (llama.cpp, LM Studio, vLLM, ...) often only understand max_tokens.
func isOfficialOpenAI(baseURL string) bool {
	b := strings.TrimSpace(baseURL)
	if b == "" {
		return true
	}
	u, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}

// modelQuirks is what one model has told us it rejects.
type modelQuirks struct {
	// tokenParam is the field used for the output cap: paramMaxTokens or
	// paramMaxCompletionTokens. Empty means "use the endpoint default". It is
	// switched at most once: a model that rejects both fields gets its error back
	// rather than requests that alternate between them.
	tokenParam string
	// dropped holds parameters the model rejected outright.
	dropped map[string]struct{}
}

// paramMemory records, per model, which request parameters the server rejected,
// so later requests leave them out instead of failing first. Safe for
// concurrent use.
type paramMemory struct {
	mu     sync.Mutex
	models map[string]*modelQuirks
}

func (m *paramMemory) get(model string) modelQuirks {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.models[model]
	if q == nil {
		return modelQuirks{}
	}
	out := modelQuirks{tokenParam: q.tokenParam, dropped: make(map[string]struct{}, len(q.dropped))}
	for k := range q.dropped {
		out.dropped[k] = struct{}{}
	}
	return out
}

// entry returns model's record, creating it. Caller holds m.mu.
func (m *paramMemory) entry(model string) *modelQuirks {
	if q := m.models[model]; q != nil {
		return q
	}
	if m.models == nil || len(m.models) >= maxLearnedModels {
		m.models = make(map[string]*modelQuirks)
	}
	q := &modelQuirks{dropped: map[string]struct{}{}}
	m.models[model] = q
	return q
}

// learn records that model rejected param and reports whether that changed the
// request we will send (false means a retry would fail the same way).
// defaultTokenParam is the endpoint's output-cap field before anything was learned.
func (m *paramMemory) learn(model, param, defaultTokenParam string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch param {
	case paramMaxTokens, paramMaxCompletionTokens:
		q := m.entry(model)
		if q.tokenParam != "" {
			// Already switched once; the other field was rejected too.
			return false
		}
		if defaultTokenParam != param {
			// We are not sending the field it rejected; switching would not help.
			return false
		}
		if param == paramMaxTokens {
			q.tokenParam = paramMaxCompletionTokens
		} else {
			q.tokenParam = paramMaxTokens
		}
		return true
	case paramTemperature, paramTopP, paramStreamOptions:
		q := m.entry(model)
		if _, ok := q.dropped[param]; ok {
			return false
		}
		q.dropped[param] = struct{}{}
		return true
	default:
		return false
	}
}

// apply rewrites req to avoid what req.Model is known to reject.
func (m *paramMemory) apply(req *openai.ChatCompletionRequest, defaultTokenParam string) {
	q := m.get(req.Model)
	tokenParam := q.tokenParam
	if tokenParam == "" {
		tokenParam = defaultTokenParam
	}
	limit := req.MaxTokens
	if req.MaxCompletionTokens > limit {
		limit = req.MaxCompletionTokens
	}
	req.MaxTokens, req.MaxCompletionTokens = 0, 0
	if limit > 0 {
		if tokenParam == paramMaxCompletionTokens {
			req.MaxCompletionTokens = limit
		} else {
			req.MaxTokens = limit
		}
	}
	if _, ok := q.dropped[paramTemperature]; ok {
		req.Temperature = 0
	}
	if _, ok := q.dropped[paramTopP]; ok {
		req.TopP = 0
	}
	if _, ok := q.dropped[paramStreamOptions]; ok {
		req.StreamOptions = nil
	}
}

// OpenAI words these as "Unsupported parameter: 'max_tokens' is not supported
// with this model..." and "Unsupported value: 'temperature' does not support 0.2
// with this model...".
var unsupportedParamMessage = regexp.MustCompile(`(?i)unsupported (?:parameter|value):\s*'([a-z_]+)'`)

// rejectedParams extracts which request parameters err says the model does not
// support. It returns nil for any other error.
func rejectedParams(err error) []string {
	if err == nil {
		return nil
	}
	// go-openai checks o1/o3/o4 requests locally, before sending them.
	switch {
	case errors.Is(err, openai.ErrReasoningModelMaxTokensDeprecated):
		return []string{paramMaxTokens}
	case errors.Is(err, openai.ErrReasoningModelLimitationsOther):
		return []string{paramTemperature, paramTopP}
	}
	var apiErr *openai.APIError
	if !errors.As(err, &apiErr) || apiErr == nil || apiErr.HTTPStatusCode != http.StatusBadRequest {
		return nil
	}
	if m := unsupportedParamMessage.FindStringSubmatch(apiErr.Message); m != nil {
		return []string{strings.ToLower(m[1])}
	}
	code, _ := apiErr.Code.(string)
	if (code == "unsupported_parameter" || code == "unsupported_value") && apiErr.Param != nil {
		return []string{strings.ToLower(strings.TrimSpace(*apiErr.Param))}
	}
	return nil
}

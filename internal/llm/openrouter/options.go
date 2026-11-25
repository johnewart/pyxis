// Package openrouter is an OpenRouter backend: the OpenAI-compatible client
// plus the OpenRouter `provider` routing object and served_by reporting.
package openrouter

import (
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// MergeOptions overlays per-call options on provider defaults. A per-call
// field wins only when set: non-unspecified sort, non-empty lists, non-nil
// allow_fallbacks. Inputs are not mutated. Returns nil when both are nil.
func MergeOptions(defaults, call *llmv1.OpenRouterCallOptions) *llmv1.OpenRouterCallOptions {
	if defaults == nil && call == nil {
		return nil
	}
	out := &llmv1.OpenRouterCallOptions{}
	for _, src := range []*llmv1.OpenRouterCallOptions{defaults, call} {
		if src == nil {
			continue
		}
		if src.GetSort() != llmv1.OpenRouterCallOptions_SORT_UNSPECIFIED {
			out.Sort = src.GetSort()
		}
		if len(src.GetOrder()) > 0 {
			out.Order = append([]string(nil), src.GetOrder()...)
		}
		if len(src.GetOnly()) > 0 {
			out.Only = append([]string(nil), src.GetOnly()...)
		}
		if len(src.GetIgnore()) > 0 {
			out.Ignore = unionOrdered(out.Ignore, src.GetIgnore())
		}
		if src.AllowFallbacks != nil {
			v := src.GetAllowFallbacks()
			out.AllowFallbacks = &v
		}
	}
	if len(out.Only) > 0 && len(out.Ignore) > 0 {
		only := make(map[string]bool, len(out.Only))
		for _, n := range out.Only {
			only[n] = true
		}
		var kept []string
		for _, n := range out.Ignore {
			if !only[n] {
				kept = append(kept, n)
			}
		}
		out.Ignore = kept
	}
	return out
}

// unionOrdered appends b to a, dropping duplicates and keeping first-seen order.
func unionOrdered(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var res []string
	for _, l := range [][]string{a, b} {
		for _, n := range l {
			if !seen[n] {
				seen[n] = true
				res = append(res, n)
			}
		}
	}
	return res
}

// ProviderObject builds the request `provider` object, or nil when nothing is set.
func ProviderObject(o *llmv1.OpenRouterCallOptions) map[string]any {
	if o == nil {
		return nil
	}
	m := map[string]any{}
	switch o.GetSort() {
	case llmv1.OpenRouterCallOptions_SORT_PRICE:
		m["sort"] = "price"
	case llmv1.OpenRouterCallOptions_SORT_THROUGHPUT:
		m["sort"] = "throughput"
	case llmv1.OpenRouterCallOptions_SORT_LATENCY:
		m["sort"] = "latency"
	}
	if len(o.GetOrder()) > 0 {
		m["order"] = o.GetOrder()
	}
	if len(o.GetOnly()) > 0 {
		m["only"] = o.GetOnly()
	}
	if len(o.GetIgnore()) > 0 {
		m["ignore"] = o.GetIgnore()
	}
	if o.AllowFallbacks != nil {
		m["allow_fallbacks"] = o.GetAllowFallbacks()
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

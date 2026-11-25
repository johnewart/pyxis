package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func bp(b bool) *bool { return &b }

func TestMergeOptions(t *testing.T) {
	tests := []struct {
		name     string
		def, cal *llmv1.OpenRouterCallOptions
		want     map[string]any
	}{
		{"both nil", nil, nil, nil},
		{"defaults only", &llmv1.OpenRouterCallOptions{Sort: llmv1.OpenRouterCallOptions_SORT_PRICE}, nil, map[string]any{"sort": "price"}},
		{"call overrides sort", &llmv1.OpenRouterCallOptions{Sort: llmv1.OpenRouterCallOptions_SORT_PRICE},
			&llmv1.OpenRouterCallOptions{Sort: llmv1.OpenRouterCallOptions_SORT_LATENCY}, map[string]any{"sort": "latency"}},
		{"unset call keeps defaults", &llmv1.OpenRouterCallOptions{Only: []string{"a"}, AllowFallbacks: bp(false)},
			&llmv1.OpenRouterCallOptions{}, map[string]any{"only": []string{"a"}, "allow_fallbacks": false}},
		{"call lists replace", &llmv1.OpenRouterCallOptions{Order: []string{"a"}, Ignore: []string{"x"}},
			&llmv1.OpenRouterCallOptions{Order: []string{"b"}, AllowFallbacks: bp(true)},
			map[string]any{"order": []string{"b"}, "ignore": []string{"x"}, "allow_fallbacks": true}},
		{"ignore combined deduped in order", &llmv1.OpenRouterCallOptions{Ignore: []string{"x", "y"}},
			&llmv1.OpenRouterCallOptions{Ignore: []string{"y", "z"}}, map[string]any{"ignore": []string{"x", "y", "z"}}},
		{"only removes from ignore", nil,
			&llmv1.OpenRouterCallOptions{Only: []string{"a"}, Ignore: []string{"a", "b"}},
			map[string]any{"only": []string{"a"}, "ignore": []string{"b"}}},
		{"empty", &llmv1.OpenRouterCallOptions{}, &llmv1.OpenRouterCallOptions{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProviderObject(MergeOptions(tc.def, tc.cal))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestRequestProviderAndServedBy(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"data":{"endpoints":[]}}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","provider":"DeepInfra","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "m", 0, &llmv1.OpenRouterCallOptions{Sort: llmv1.OpenRouterCallOptions_SORT_PRICE})
	r, err := c.Generate(context.Background(), "", "hello", &llmv1.CallOptions{
		Openrouter: &llmv1.OpenRouterCallOptions{Only: []string{"DeepInfra"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.GetServedBy() != "DeepInfra" {
		t.Fatalf("served_by=%q", r.GetServedBy())
	}
	want := map[string]any{"sort": "price", "only": []any{"DeepInfra"}}
	if !reflect.DeepEqual(gotBody["provider"], want) {
		t.Fatalf("provider=%#v", gotBody["provider"])
	}
}

func TestStreamServedBy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"provider\":\"Together\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	c := New("k", srv.URL, "m", 0, nil)
	var served string
	err := c.ChatStream(context.Background(), []*llmv1.Message{{Role: "user", Content: "hello"}}, nil, nil, func(f *llmv1.ConversationStreamFrame) error {
		if sc := f.GetStreamComplete(); sc != nil {
			served = sc.GetServedBy()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if served != "Together" {
		t.Fatalf("served=%q", served)
	}
}

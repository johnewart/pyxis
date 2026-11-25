package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// syncBuffer is a goroutine-safe buffer for capturing log output.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestGenerateFailsOpenAndLogsProbeFailure(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "http 500", status: http.StatusInternalServerError, body: `oops`},
		{name: "bad json", status: http.StatusOK, body: `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := &syncBuffer{}
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			var mu sync.Mutex
			var chatBody map[string]any
			var probes int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					mu.Lock()
					probes++
					mu.Unlock()
					if !strings.HasSuffix(r.URL.Path, "/models/m/endpoints") {
						t.Errorf("unexpected probe path %q", r.URL.Path)
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				if err := json.Unmarshal(raw, &chatBody); err != nil {
					t.Errorf("decode chat body: %v", err)
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"x","provider":"Any","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
			}))
			defer srv.Close()

			c := New("k", srv.URL, "m", 0, nil)
			reply, err := c.Generate(context.Background(), "", "hello", &llmv1.CallOptions{}, nil)
			if err != nil {
				t.Fatalf("Generate must succeed when probe fails: %v", err)
			}
			if reply == nil {
				t.Fatal("nil reply")
			}

			mu.Lock()
			defer mu.Unlock()
			if probes == 0 {
				t.Fatal("expected the endpoints probe to be attempted")
			}
			if chatBody == nil {
				t.Fatal("chat request was not sent")
			}
			if p, ok := chatBody["provider"].(map[string]any); ok {
				if _, has := p["ignore"]; has {
					t.Fatalf("chat body must not carry ignore list: %#v", p)
				}
			}
			logs := buf.String()
			if !strings.Contains(logs, "endpoint health probe failed") || !strings.Contains(logs, "level=WARN") {
				t.Fatalf("probe failure not logged; logs=%q", logs)
			}
		})
	}
}

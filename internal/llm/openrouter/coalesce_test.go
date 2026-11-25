package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestConcurrentProbesCoalesce(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		_, _ = io.WriteString(w, `{"data":{"endpoints":[{"provider_name":"Bad","status":-1}]}}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "a/b", 0, nil)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := c.EndpointHealth(context.Background(), "a/b")
			if err == nil && !reflect.DeepEqual(st.Deranked, []string{"Bad"}) {
				t.Errorf("deranked=%v", st.Deranked)
			}
			errs <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("caller error: %v", err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1", hits.Load())
	}
}

func TestEndpointHealthFetchedAtAndInjectedTransport(t *testing.T) {
	now := time.Unix(5000, 0)
	c := New("k", "http://example.invalid/api", "m", 0, nil).WithClock(func() time.Time { return now })
	var viaTransport atomic.Int32
	c.prober.hc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		viaTransport.Add(1)
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{"endpoints":[]}}`)), Header: http.Header{}}, nil
	})}
	st, err := c.EndpointHealth(context.Background(), "m")
	if err != nil || !st.FetchedAt.Equal(now) || viaTransport.Load() != 1 {
		t.Fatalf("st=%+v err=%v via=%d", st, err, viaTransport.Load())
	}
}

func TestRequestIgnoreMergedButNeverOnly(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"data":{"endpoints":[{"provider_name":"Bad","status":-1},{"provider_name":"Keep","status":-1}]}}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","provider":"Keep","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := New("k", srv.URL, "m", 0, nil)
	_, err := c.Generate(context.Background(), "", "hello", &llmv1.CallOptions{
		Openrouter: &llmv1.OpenRouterCallOptions{Only: []string{"Keep"}, Ignore: []string{"User"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := gotBody["provider"].(map[string]any)
	if !reflect.DeepEqual(p["ignore"], []any{"User", "Bad"}) || !reflect.DeepEqual(p["only"], []any{"Keep"}) {
		t.Fatalf("provider=%#v", p)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestDerankedProvidersCacheAndTTL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/models/a/b/endpoints" {
			t.Errorf("path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"endpoints":[{"provider_name":"Bad","status":-2},{"provider_name":"Good","status":0}]}}`))
	}))
	defer srv.Close()

	c := New("k", srv.URL, "a/b", 0, nil)
	now := time.Unix(1000, 0)
	c.WithClock(func() time.Time { return now })
	c.WithEndpointHealth(ResolveEndpointHealth(nil, 10))

	for i := 0; i < 2; i++ {
		got, err := c.DerankedProviders(context.Background(), "a/b")
		if err != nil || !reflect.DeepEqual(got, []string{"Bad"}) {
			t.Fatalf("got %v, %v", got, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1", hits.Load())
	}
	now = now.Add(11 * time.Second)
	if _, err := c.DerankedProviders(context.Background(), "a/b"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits=%d want 2", hits.Load())
	}

	f := false
	c.WithEndpointHealth(ResolveEndpointHealth(&f, 10))
	now = now.Add(time.Hour)
	if got, err := c.DerankedProviders(context.Background(), "a/b"); err != nil || got != nil || hits.Load() != 2 {
		t.Fatalf("disabled: %v %v hits=%d", got, err, hits.Load())
	}
}

func TestProviderWithHealthMergeAndFailOpen(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"endpoints":[{"provider_name":"Bad","status":-1},{"provider_name":"Keep","status":-1}]}}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, "m", 0, nil)
	tr := &transport{client: c}
	st := &callState{opts: &llmv1.OpenRouterCallOptions{Only: []string{"Keep"}}}
	st.provider = ProviderObject(st.opts)
	got := tr.providerWithHealth(context.Background(), []byte(`{"model":"m"}`), st)
	if !reflect.DeepEqual(got["ignore"], []string{"Bad"}) {
		t.Fatalf("ignore=%v", got["ignore"])
	}

	c2 := New("k", srv.URL, "m", 0, nil)
	status = http.StatusInternalServerError
	tr2 := &transport{client: c2}
	got = tr2.providerWithHealth(context.Background(), []byte(`{"model":"m"}`), st)
	if _, ok := got["ignore"]; ok {
		t.Fatalf("fail-open should not add ignore: %v", got)
	}
}

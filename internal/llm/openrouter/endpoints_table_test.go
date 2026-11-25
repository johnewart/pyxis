package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEndpointHealthTable(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    []string
		wantErr bool
	}{
		{"negative status deranked", 200, `{"data":{"endpoints":[{"provider_name":"A","status":-1},{"provider_name":"B","status":0},{"provider_name":"C","status":-5}]}}`, []string{"A", "C"}, false},
		{"all healthy", 200, `{"data":{"endpoints":[{"provider_name":"A","status":0}]}}`, nil, false},
		{"null or missing status ignored", 200, `{"data":{"endpoints":[{"provider_name":"A","status":null},{"provider_name":"B"}]}}`, nil, false},
		{"empty provider name ignored", 200, `{"data":{"endpoints":[{"provider_name":"","status":-1}]}}`, nil, false},
		{"no endpoints", 200, `{"data":{"endpoints":[]}}`, nil, false},
		{"server error", 500, ``, nil, true},
		{"not found", 404, `{}`, nil, true},
		{"invalid json", 200, `{not json`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Authorization") != "Bearer k" {
					t.Errorf("auth header %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			now := time.Unix(1000, 0)
			c := New("k", srv.URL, "a/b", 0, nil).WithClock(func() time.Time { return now })
			st, err := c.EndpointHealth(context.Background(), "a/b")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(st.Deranked, tc.want) {
				t.Fatalf("deranked=%v want %v", st.Deranked, tc.want)
			}
			if !tc.wantErr && !st.FetchedAt.Equal(now) {
				t.Fatalf("fetchedAt=%v want %v", st.FetchedAt, now)
			}

			// Successes are cached within the TTL; failures are not.
			_, _ = c.EndpointHealth(context.Background(), "a/b")
			wantHits := int32(1)
			if tc.wantErr {
				wantHits = 2
			}
			if hits.Load() != wantHits {
				t.Fatalf("hits=%d want %d", hits.Load(), wantHits)
			}
		})
	}
}

func TestEndpointHealthPerModelCacheAndExpiry(t *testing.T) {
	var hits atomic.Int32
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":{"endpoints":[]}}`))
	}))
	defer srv.Close()

	now := time.Unix(1000, 0)
	c := New("k", srv.URL, "a/b", 0, nil).WithClock(func() time.Time { return now })
	c.WithEndpointHealth(ResolveEndpointHealth(nil, 30))
	ctx := context.Background()

	for _, m := range []string{"a/b", "c/d", "a/b", "c/d"} {
		if _, err := c.EndpointHealth(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	if hits.Load() != 2 || !reflect.DeepEqual(gotPaths, []string{"/models/a/b/endpoints", "/models/c/d/endpoints"}) {
		t.Fatalf("hits=%d paths=%v", hits.Load(), gotPaths)
	}

	now = now.Add(29 * time.Second)
	if _, err := c.EndpointHealth(ctx, "a/b"); err != nil || hits.Load() != 2 {
		t.Fatalf("before expiry: err=%v hits=%d", err, hits.Load())
	}
	now = now.Add(2 * time.Second)
	st, err := c.EndpointHealth(ctx, "a/b")
	if err != nil || hits.Load() != 3 || !st.FetchedAt.Equal(now) {
		t.Fatalf("after expiry: err=%v hits=%d fetchedAt=%v", err, hits.Load(), st.FetchedAt)
	}
}

func TestEndpointHealthDisabledOrNoModelMakesNoProbe(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	off := false
	tests := []struct {
		name  string
		opts  EndpointHealthOptions
		model string
	}{
		{"disabled", ResolveEndpointHealth(&off, 0), "a/b"},
		{"empty model", ResolveEndpointHealth(nil, 0), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := New("k", srv.URL, "a/b", 0, nil).WithEndpointHealth(tc.opts)
			st, err := c.EndpointHealth(context.Background(), tc.model)
			if err != nil || len(st.Deranked) != 0 || !st.FetchedAt.IsZero() || st.Model != tc.model {
				t.Fatalf("st=%+v err=%v", st, err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("hits=%d want 0", hits.Load())
	}
}

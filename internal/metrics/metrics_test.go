package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain exposition format", ct)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestHandler_servesRuntimeAndProcessCollectors(t *testing.T) {
	body := scrape(t, New().Handler())
	for _, want := range []string{"go_goroutines", "process_start_time_seconds", "promhttp_metric_handler_requests_total"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %s", want)
		}
	}
}

func TestRegisterer_collectorsAppearInScrape(t *testing.T) {
	m := New()
	c := prometheus.NewCounter(prometheus.CounterOpts{Namespace: Namespace, Name: "test_total", Help: "test"})
	m.Registerer().MustRegister(c)
	c.Add(3)
	if body := scrape(t, m.Handler()); !strings.Contains(body, "llmd_test_total 3") {
		t.Fatalf("scrape missing llmd_test_total 3:\n%s", body)
	}
}

// Two runtimes in one process must not share or collide on a registry.
func TestNew_registriesAreIndependent(t *testing.T) {
	a, b := New(), New()
	c := prometheus.NewCounter(prometheus.CounterOpts{Namespace: Namespace, Name: "only_in_a_total", Help: "test"})
	a.Registerer().MustRegister(c)
	if strings.Contains(scrape(t, b.Handler()), "llmd_only_in_a_total") {
		t.Fatal("collector registered on one Metrics leaked into another")
	}
}

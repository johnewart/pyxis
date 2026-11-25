package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveMetricsAddr(t *testing.T) {
	cases := []struct {
		name, explicit     string
		debug              bool
		wantAddr, wantTrig string
	}{
		{"off by default", "", false, "", ""},
		{"dev mode picks an ephemeral loopback port", "", true, "127.0.0.1:0", "LLMD_DEBUG=1"},
		{"override wins without debug", "0.0.0.0:9464", false, "0.0.0.0:9464", envMetricsAddr},
		{"override wins over dev mode", " 127.0.0.1:9464 ", true, "127.0.0.1:9464", envMetricsAddr},
		{"blank override is unset", "   ", false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, trig := resolveMetricsAddr(c.explicit, c.debug)
			if addr != c.wantAddr || trig != c.wantTrig {
				t.Fatalf("resolveMetricsAddr(%q, %v) = (%q, %q), want (%q, %q)",
					c.explicit, c.debug, addr, trig, c.wantAddr, c.wantTrig)
			}
		})
	}
}

// The metrics listener may be exposed off-box, so it must serve /metrics and
// nothing else (in particular no pprof or statusz).
func TestMetricsMux_servesOnlyMetrics(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(metricsMux(inner))
	defer srv.Close()
	for path, want := range map[string]int{
		"/metrics":       http.StatusOK,
		"/debug/pprof/":  http.StatusNotFound,
		"/debug/statusz": http.StatusNotFound,
		"/":              http.StatusNotFound,
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

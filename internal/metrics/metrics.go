// Package metrics owns llmd's Prometheus registry and the HTTP handler that
// exposes it in the Prometheus text format.
//
// The registry is per Metrics value, never prometheus.DefaultRegisterer: a
// process embedding several llmd runtimes (see llmdhost) must not have them
// collide on, or silently share, a process-wide global. The Host owns one
// Metrics and passes it to every component it instruments.
package metrics

import (
	"net/http"

	"github.com/johnewart/pyxis/llmdlog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// LogModule tags this package's log lines.
const LogModule = "metrics"

// Namespace prefixes every llmd metric name (llmd_...).
const Namespace = "llmd"

// Metrics is one llmd runtime's metric registry.
type Metrics struct {
	reg *prometheus.Registry
}

// New builds a registry carrying the Go runtime and process collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &Metrics{reg: reg}
}

// Registerer is where llmd components register their collectors.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Gatherer exposes the registry for tests and for hosts that merge llmd's
// metrics into their own endpoint.
func (m *Metrics) Gatherer() prometheus.Gatherer { return m.reg }

// Handler serves the registry in the Prometheus exposition format. A collector
// that fails is logged and the rest of the scrape is still served, so one bad
// collector never blanks the whole endpoint.
func (m *Metrics) Handler() http.Handler {
	// InstrumentMetricHandler adds promhttp_metric_handler_requests_total and
	// _in_flight; HandlerOpts.Registry adds _errors_total. Together they show
	// whether scrapes themselves are failing.
	return promhttp.InstrumentMetricHandler(m.reg, promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		ErrorLog:      errorLogger{},
		ErrorHandling: promhttp.ContinueOnError,
		Registry:      m.reg,
	}))
}

// errorLogger adapts promhttp's error log to llmdlog.
type errorLogger struct{}

func (errorLogger) Println(v ...interface{}) {
	llmdlog.Stdout(LogModule, llmdlog.LevelWarn, "metrics scrape: %v", v)
}

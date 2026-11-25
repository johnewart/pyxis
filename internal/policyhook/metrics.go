package policyhook

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds the policy hook Prometheus collectors. A nil *Metrics is a
// valid no-op.
type Metrics struct {
	calls    *prometheus.CounterVec
	duration *prometheus.HistogramVec
	filtered prometheus.Counter
}

// NewMetrics creates the collectors and registers them on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llmd_policy_hook_calls_total",
			Help: "Policy hook calls by hook, decision and outcome (ok, error, timeout).",
		}, []string{"hook", "decision", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "llmd_policy_hook_duration_seconds",
			Help:    "Policy hook call duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"hook"}),
		filtered: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "llmd_policy_tools_filtered_total",
			Help: "Tools removed by the FilterTools hook.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.calls, m.duration, m.filtered)
	}
	return m
}

func (m *Metrics) observe(hook string, d Decision, outcome string, seconds float64) {
	if m == nil {
		return
	}
	m.calls.WithLabelValues(hook, d.String(), outcome).Inc()
	m.duration.WithLabelValues(hook).Observe(seconds)
}

func (m *Metrics) toolsFiltered(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.filtered.Add(float64(n))
}

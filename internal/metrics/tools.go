package metrics

import (
	"time"

	"github.com/johnewart/pyxis/toolseam"
	"github.com/prometheus/client_golang/prometheus"
)

// unregisteredTool labels calls that named no registered tool. The
// model-supplied name is never used as a label value: it is arbitrary input
// and would make the label set unbounded.
const unregisteredTool = "_unregistered"

// durationBuckets span 1ms (inline tools) to 10min (long delegated or
// executor-backed tools).
var durationBuckets = []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}

// resultBytesBuckets span 64B to 4MiB.
var resultBytesBuckets = prometheus.ExponentialBuckets(64, 4, 10)

// Tools records every tool dispatch through a toolseam.Handlers set. It is a
// toolseam.InvocationObserver.
type Tools struct {
	invocations *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	resultBytes *prometheus.HistogramVec
	inflight    *prometheus.GaugeVec
}

var _ toolseam.InvocationObserver = (*Tools)(nil)

// NewTools registers the tool metrics on m.
func NewTools(m *Metrics) *Tools {
	t := &Tools{
		invocations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace, Subsystem: "tool", Name: "invocations_total",
			Help: "Tool dispatches that finished, by tool, origin (chat_loop, mcp, invoke_tool), binding and outcome.",
		}, []string{"tool", "origin", "binding", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Subsystem: "tool", Name: "duration_seconds",
			Help:    "Time spent executing a tool (dispatch to result), by tool, origin, binding and outcome.",
			Buckets: durationBuckets,
		}, []string{"tool", "origin", "binding", "outcome"}),
		resultBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace, Subsystem: "tool", Name: "result_bytes",
			Help:    "Size of the content a tool returned, by tool and origin.",
			Buckets: resultBytesBuckets,
		}, []string{"tool", "origin"}),
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace, Subsystem: "tool", Name: "inflight",
			Help: "Tool dispatches currently running, by tool and origin.",
		}, []string{"tool", "origin"}),
	}
	m.reg.MustRegister(t.invocations, t.duration, t.resultBytes, t.inflight)
	return t
}

// Begin implements toolseam.InvocationObserver.
func (t *Tools) Begin(tool string, origin toolseam.Origin, binding toolseam.Binding) func(toolseam.InvocationResult) {
	if tool == "" {
		tool = unregisteredTool
	}
	o := origin.String()
	inflight := t.inflight.WithLabelValues(tool, o)
	inflight.Inc()
	start := time.Now()
	return func(r toolseam.InvocationResult) {
		inflight.Dec()
		outcome := string(r.Outcome)
		t.invocations.WithLabelValues(tool, o, string(binding), outcome).Inc()
		t.duration.WithLabelValues(tool, o, string(binding), outcome).Observe(time.Since(start).Seconds())
		t.resultBytes.WithLabelValues(tool, o).Observe(float64(r.ResultBytes))
	}
}

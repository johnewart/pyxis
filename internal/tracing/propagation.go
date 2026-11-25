package tracing

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

// HTTPTransport wraps base (http.DefaultTransport when nil) so outgoing
// requests carry W3C traceparent/tracestate for the span in the request
// context. tp may be nil: the transport then creates no spans of its own and
// only propagates the caller's context. No process-wide global is read.
func HTTPTransport(base http.RoundTripper, tp trace.TracerProvider) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	return otelhttp.NewTransport(base,
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithPropagators(Propagator()))
}

// ClientStatsHandler is the otelgrpc handler for outgoing gRPC clients.
func ClientStatsHandler(tp trace.TracerProvider) stats.Handler {
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	return otelgrpc.NewClientHandler(
		otelgrpc.WithTracerProvider(tp),
		otelgrpc.WithPropagators(Propagator()))
}

// ServerStatsHandler is the otelgrpc handler for incoming gRPC calls; it
// continues an incoming W3C trace.
func ServerStatsHandler(tp trace.TracerProvider) stats.Handler {
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	return otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(tp),
		otelgrpc.WithPropagators(Propagator()))
}

// ClientDialOption installs ClientStatsHandler on a gRPC client.
func ClientDialOption(tp trace.TracerProvider) grpc.DialOption {
	return grpc.WithStatsHandler(ClientStatsHandler(tp))
}

// ServerOption installs ServerStatsHandler on a gRPC server.
func ServerOption(tp trace.TracerProvider) grpc.ServerOption {
	return grpc.StatsHandler(ServerStatsHandler(tp))
}

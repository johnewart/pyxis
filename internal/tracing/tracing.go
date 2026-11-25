// Package tracing sets up OpenTelemetry tracing for llmd. The provider is
// owned by the caller (the Host); no process-wide global is installed.
package tracing

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	// DefaultServiceName is used when no service name is configured.
	DefaultServiceName  = "llmd"
	instrumentationName = "github.com/johnewart/pyxis"

	ProtocolGRPC = "grpc"
	ProtocolHTTP = "http/protobuf"
)

// Config configures tracing. An empty Endpoint disables tracing.
type Config struct {
	// Endpoint is the OTLP endpoint (host:port or URL).
	Endpoint string
	// Protocol is "grpc" (default) or "http/protobuf".
	Protocol string
	// Headers are extra OTLP request headers.
	Headers map[string]string
	// Insecure disables TLS to the endpoint.
	Insecure bool
	// ServiceName is the service.name resource attribute (default "llmd").
	ServiceName string
}

// Provider owns a tracer provider and its optional exporter.
type Provider struct {
	tp       trace.TracerProvider
	sdk      *sdktrace.TracerProvider
	exporter sdktrace.SpanExporter
}

// New builds a Provider. With an empty Endpoint it returns a no-op provider
// and creates no exporter. Extra sdktrace options (for example a span
// recorder) are applied only when tracing is enabled.
func New(ctx context.Context, cfg Config, opts ...sdktrace.TracerProviderOption) (*Provider, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return &Provider{tp: noop.NewTracerProvider()}, nil
	}
	exp, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}
	name := cfg.ServiceName
	if name == "" {
		name = DefaultServiceName
	}
	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(name)))
	if err != nil {
		if sErr := exp.Shutdown(ctx); sErr != nil {
			err = fmt.Errorf("%w (exporter shutdown: %v)", err, sErr)
		}
		return nil, fmt.Errorf("tracing: build resource: %w", err)
	}
	all := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	}
	all = append(all, opts...)
	sdk := sdktrace.NewTracerProvider(all...)
	return &Provider{tp: sdk, sdk: sdk, exporter: exp}, nil
}

func newExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	isURL := strings.Contains(cfg.Endpoint, "://")
	switch strings.ToLower(strings.TrimSpace(cfg.Protocol)) {
	case "", ProtocolGRPC:
		o := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
		if isURL {
			o = []otlptracegrpc.Option{otlptracegrpc.WithEndpointURL(cfg.Endpoint)}
		}
		if cfg.Insecure {
			o = append(o, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			o = append(o, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		return otlptracegrpc.New(ctx, o...)
	case ProtocolHTTP, "http":
		o := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
		if isURL {
			o = []otlptracehttp.Option{otlptracehttp.WithEndpointURL(cfg.Endpoint)}
		}
		if cfg.Insecure {
			o = append(o, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			o = append(o, otlptracehttp.WithHeaders(cfg.Headers))
		}
		return otlptracehttp.New(ctx, o...)
	default:
		return nil, fmt.Errorf("unsupported OTLP protocol %q", cfg.Protocol)
	}
}

// Enabled reports whether spans are exported.
func (p *Provider) Enabled() bool { return p != nil && p.sdk != nil }

// Exporter returns the span exporter, or nil when tracing is disabled.
func (p *Provider) Exporter() sdktrace.SpanExporter {
	if p == nil {
		return nil
	}
	return p.exporter
}

// TracerProvider returns the tracer provider (no-op when disabled).
func (p *Provider) TracerProvider() trace.TracerProvider {
	if p == nil || p.tp == nil {
		return noop.NewTracerProvider()
	}
	return p.tp
}

// Tracer returns llmd's tracer.
func (p *Provider) Tracer() trace.Tracer {
	return p.TracerProvider().Tracer(instrumentationName)
}

// Shutdown flushes and stops the provider. Safe on a disabled provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.sdk == nil {
		return nil
	}
	if err := p.sdk.Shutdown(ctx); err != nil {
		return fmt.Errorf("tracing: shutdown: %w", err)
	}
	return nil
}

// Propagator returns the W3C trace-context propagator only.
func Propagator() propagation.TextMapPropagator {
	return propagation.TraceContext{}
}

// FromEnv builds a Config from standard OTEL_* environment variables.
func FromEnv() Config {
	cfg := Config{ServiceName: DefaultServiceName}
	if v := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")); v != "" {
		cfg.ServiceName = v
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		return cfg
	}
	cfg.Endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if cfg.Endpoint == "" {
		cfg.Endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	cfg.Protocol = strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")))
	cfg.Insecure = strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")), "true")
	cfg.Headers = parseHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"))
	return cfg
}

func parseHeaders(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

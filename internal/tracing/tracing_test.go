package tracing

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestNewWithoutEndpointIsDisabled(t *testing.T) {
	p, err := New(context.Background(), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("New returned nil provider")
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	if p.Enabled() {
		t.Fatal("Enabled() = true with empty endpoint")
	}
	if p.Exporter() != nil {
		t.Fatal("an exporter was created without an endpoint")
	}
	_, span := p.Tracer().Start(context.Background(), "noop")
	span.End()
	if span.SpanContext().IsValid() {
		t.Fatal("no-op tracer produced a valid span context")
	}
}

func TestPropagatorIsTraceContextOnly(t *testing.T) {
	got := Propagator().Fields()
	sort.Strings(got)
	want := []string{"traceparent", "tracestate"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %v, want %v", got, want)
	}
}

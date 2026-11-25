package server

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Span outcome values for the llmd.outcome attribute.
const (
	outcomeOK       = "ok"
	outcomeError    = "error"
	outcomeCanceled = "canceled"
)

// startSpan starts a span on the server's Tracer. With a nil Tracer it returns
// ctx unchanged and a nil span; endSpan accepts a nil span.
func (s *LlmServer) startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if s == nil || s.Tracer == nil {
		return ctx, nil
	}
	return s.Tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// endSpan records the outcome (never message content) and ends the span.
// reqCtx is the context whose cancellation marks the outcome as canceled.
func endSpan(span trace.Span, reqCtx context.Context, err error) {
	if span == nil {
		return
	}
	outcome := outcomeOK
	if err != nil {
		if reqCtx.Err() != nil || errors.Is(err, context.Canceled) || status.Code(err) == grpccodes.Canceled {
			outcome = outcomeCanceled
		} else {
			outcome = outcomeError
			span.RecordError(err)
			span.SetStatus(codes.Error, outcome)
		}
	}
	span.SetAttributes(attribute.String("llmd.outcome", outcome))
	span.End()
}

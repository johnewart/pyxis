// Package grpcutf8 keeps invalid UTF-8 from failing a gRPC call.
//
// A proto3 string field must be valid UTF-8, and gRPC refuses to marshal a
// message that holds one that is not: the WHOLE call fails ("grpc: error while
// marshaling: string field contains invalid UTF-8"). For a streamed chat turn
// that means the turn dies. The bytes come from places no one validates -
// a tool reading a binary file, a command printing one, a string cut at a
// byte offset in the middle of a multi-byte character - and checking each
// source is a game of whack-a-mole. So every message is checked once, where
// it leaves: on send, its string fields are made valid (invalid sequences
// become U+FFFD). A message that is already valid - nearly all of them - is
// walked and left untouched.
package grpcutf8

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/johnewart/pyxis/internal/tracing"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const replacement = "�"

// Sanitize makes every string field of m (nested messages, lists and maps
// included) valid UTF-8, in place. It reports whether it changed anything.
func Sanitize(m proto.Message) bool {
	if m == nil {
		return false
	}
	return sanitize(m.ProtoReflect())
}

func sanitize(m protoreflect.Message) bool {
	if !m.IsValid() {
		return false
	}
	changed := false
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList():
			l := v.List()
			for i := 0; i < l.Len(); i++ {
				switch fd.Kind() {
				case protoreflect.StringKind:
					if s := l.Get(i).String(); !utf8.ValidString(s) {
						l.Set(i, protoreflect.ValueOfString(strings.ToValidUTF8(s, replacement)))
						changed = true
					}
				case protoreflect.MessageKind, protoreflect.GroupKind:
					if sanitize(l.Get(i).Message()) {
						changed = true
					}
				}
			}
		case fd.IsMap():
			mp := v.Map()
			var fixKeys []protoreflect.MapKey
			mp.Range(func(k protoreflect.MapKey, mv protoreflect.Value) bool {
				if fd.MapKey().Kind() == protoreflect.StringKind && !utf8.ValidString(k.String()) {
					fixKeys = append(fixKeys, k)
				}
				switch fd.MapValue().Kind() {
				case protoreflect.StringKind:
					if s := mv.String(); !utf8.ValidString(s) {
						mp.Set(k, protoreflect.ValueOfString(strings.ToValidUTF8(s, replacement)))
						changed = true
					}
				case protoreflect.MessageKind, protoreflect.GroupKind:
					if sanitize(mv.Message()) {
						changed = true
					}
				}
				return true
			})
			for _, k := range fixKeys {
				val := mp.Get(k)
				mp.Clear(k)
				mp.Set(protoreflect.ValueOfString(strings.ToValidUTF8(k.String(), replacement)).MapKey(), val)
				changed = true
			}
		case fd.Kind() == protoreflect.StringKind:
			if s := v.String(); !utf8.ValidString(s) {
				m.Set(fd, protoreflect.ValueOfString(strings.ToValidUTF8(s, replacement)))
				changed = true
			}
		case fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind:
			if sanitize(v.Message()) {
				changed = true
			}
		}
		return true
	})
	return changed
}

func sanitizeAny(v any) {
	if m, ok := v.(proto.Message); ok {
		Sanitize(m)
	}
}

// UnaryServerInterceptor sanitizes every unary response.
func UnaryServerInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	resp, err := h(ctx, req)
	sanitizeAny(resp)
	return resp, err
}

// StreamServerInterceptor sanitizes every message a server stream sends.
func StreamServerInterceptor(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	return h(srv, serverStream{ss})
}

type serverStream struct{ grpc.ServerStream }

func (s serverStream) SendMsg(m any) error {
	sanitizeAny(m)
	return s.ServerStream.SendMsg(m)
}

// UnaryClientInterceptor sanitizes every unary request.
func UnaryClientInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	sanitizeAny(req)
	return invoker(ctx, method, req, reply, cc, opts...)
}

// StreamClientInterceptor sanitizes every message a client stream sends.
func StreamClientInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	cs, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		return nil, err
	}
	return clientStream{cs}, nil
}

type clientStream struct{ grpc.ClientStream }

func (c clientStream) SendMsg(m any) error {
	sanitizeAny(m)
	return c.ClientStream.SendMsg(m)
}

// ServerOptions installs the server interceptors.
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(UnaryServerInterceptor),
		grpc.ChainStreamInterceptor(StreamServerInterceptor),
	}
}

// DialOptions installs the client interceptors.
func DialOptions() []grpc.DialOption {
	return DialOptionsWithTracer(nil)
}

// DialOptionsWithTracer installs the client interceptors and exactly one
// otelgrpc client stats handler using tp (nil = propagation only). Do not
// add another tracing.ClientDialOption on top: each handler injects its own
// traceparent.
func DialOptionsWithTracer(tp trace.TracerProvider) []grpc.DialOption {
	return []grpc.DialOption{
		// Outgoing calls carry W3C traceparent for the caller's span.
		tracing.ClientDialOption(tp),
		grpc.WithChainUnaryInterceptor(UnaryClientInterceptor),
		grpc.WithChainStreamInterceptor(StreamClientInterceptor),
	}
}

package serve

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// DebugUnaryInterceptor logs unary LlmService RPCs when chat debugging is on
// (not other gRPC services).
func DebugUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if !ChatDebugEnabled() || !isLlmServiceMethod(info.FullMethod) {
		return handler(ctx, req)
	}
	start := time.Now()
	DebugChatFields("unary_begin", map[string]any{"method": info.FullMethod},
		"unary begin %s %s", info.FullMethod, debugGRPCCallContext(ctx))
	if pm, ok := req.(proto.Message); ok {
		DebugChatFields("unary_call", map[string]any{"method": info.FullMethod, "req": protoDebugJSON(pm)},
			"unary call %s req=%s", info.FullMethod, protoDebugJSON(pm))
	} else {
		debugChatf("unary_call", "unary call %s req_type=%T", info.FullMethod, req)
	}
	resp, err := handler(ctx, req)
	dur := time.Since(start)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			DebugChatFields("unary_done",
				map[string]any{"method": info.FullMethod, "error": err.Error(), "code": st.Code().String(), "duration_ms": dur.Milliseconds()},
				"unary done %s err=%v code=%s msg=%q dur=%s", info.FullMethod, err, st.Code(), st.Message(), dur)
		} else {
			DebugChatFields("unary_done",
				map[string]any{"method": info.FullMethod, "error": err.Error(), "duration_ms": dur.Milliseconds()},
				"unary done %s err=%v dur=%s", info.FullMethod, err, dur)
		}
		return resp, err
	}
	if resp == nil {
		DebugChatFields("unary_done", map[string]any{"method": info.FullMethod, "duration_ms": dur.Milliseconds()},
			"unary done %s resp=<nil> dur=%s", info.FullMethod, dur)
		return resp, err
	}
	if pm, ok := resp.(proto.Message); ok {
		DebugChatFields("unary_done",
			map[string]any{"method": info.FullMethod, "resp": protoDebugJSON(pm), "duration_ms": dur.Milliseconds()},
			"unary done %s resp=%s dur=%s", info.FullMethod, protoDebugJSON(pm), dur)
	} else {
		debugChatf("unary_done", "unary done %s resp_type=%T dur=%s", info.FullMethod, resp, dur)
	}
	return resp, err
}

type debugStreamWrapper struct {
	grpc.ServerStream
	method string
}

func (w *debugStreamWrapper) SendMsg(m any) error {
	// Per-message bodies are the raw streaming firehose: gate behind
	// LLMD_DEBUG_TRAFFIC=1.
	if TrafficDebugEnabled() {
		if pm, ok := m.(proto.Message); ok {
			DebugChatFields("stream_send", map[string]any{"method": w.method, "msg": protoDebugJSON(pm)},
				"stream send %s msg=%s", w.method, protoDebugJSON(pm))
		} else {
			debugChatf("stream_send", "stream send %s type=%T", w.method, m)
		}
	}
	return w.ServerStream.SendMsg(m)
}

func (w *debugStreamWrapper) RecvMsg(m any) error {
	err := w.ServerStream.RecvMsg(m)
	if err == nil && TrafficDebugEnabled() {
		if pm, ok := m.(proto.Message); ok {
			DebugChatFields("stream_recv", map[string]any{"method": w.method, "msg": protoDebugJSON(pm)},
				"stream recv %s msg=%s", w.method, protoDebugJSON(pm))
		}
	}
	return err
}

// DebugStreamInterceptor logs LlmService stream lifecycle, and each
// SendMsg/RecvMsg under LLMD_DEBUG_TRAFFIC, when chat debugging is on.
func DebugStreamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if !ChatDebugEnabled() || !isLlmServiceMethod(info.FullMethod) {
		return handler(srv, ss)
	}
	start := time.Now()
	debugChatf(
		"stream_begin",
		"stream begin %s %s is_client_stream=%v is_server_stream=%v",
		info.FullMethod,
		debugGRPCCallContext(ss.Context()),
		info.IsClientStream,
		info.IsServerStream,
	)
	wrapped := &debugStreamWrapper{ServerStream: ss, method: info.FullMethod}
	err := handler(srv, wrapped)
	dur := time.Since(start)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			DebugChatFields("stream_close",
				map[string]any{"method": info.FullMethod, "error": err.Error(), "code": st.Code().String(), "duration_ms": dur.Milliseconds()},
				"stream close %s err=%v code=%s msg=%q dur=%s", info.FullMethod, err, st.Code(), st.Message(), dur)
		} else {
			DebugChatFields("stream_close",
				map[string]any{"method": info.FullMethod, "error": err.Error(), "duration_ms": dur.Milliseconds()},
				"stream close %s err=%v dur=%s", info.FullMethod, err, dur)
		}
		return err
	}
	DebugChatFields("stream_close", map[string]any{"method": info.FullMethod, "duration_ms": dur.Milliseconds()},
		"stream close %s ok dur=%s", info.FullMethod, dur)
	return nil
}

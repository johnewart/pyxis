package serve

// ConnectAuth: the renderer-facing grpc-web/Connect surface.
//
// Watch is read-only, but it is still a direct line into llmd from a browser
// context, so it is only reachable with the per-spawn shared secret and from an
// allowlisted origin.
//
// This test drives a locally-declared core echo service rather than the
// host-registered service: the auth interceptor under test has nothing
// to do with workspace semantics, so a minimal streaming service proves the
// same thing without pulling llmd/internal/serve across the module boundary.

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const connectTestOrigin = "http://127.0.0.1:5173"
const connectTestToken = "s3cret-per-spawn-token"

// echoWatchServer implements a single streaming RPC that echoes back one
// snapshot message carrying the requested id, standing in for
// WorkspaceService.Watch for the purposes of this auth test.
type echoWatchServer struct{}

func (echoWatchServer) watch(req *wrapperspb.StringValue, stream grpc.ServerStream) error {
	return stream.SendMsg(&wrapperspb.StringValue{Value: req.GetValue()})
}

func echoWatchHandler(srv interface{}, stream grpc.ServerStream) error {
	req := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(req); err != nil {
		return err
	}
	return srv.(*echoWatchServer).watch(req, stream)
}

var echoServiceDesc = grpc.ServiceDesc{
	ServiceName: "test.v1.EchoService",
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Watch",
			Handler:       echoWatchHandler,
			ServerStreams: true,
		},
	},
	Metadata: "connect_auth_test.go",
}

// newConnectTestServer serves the echo service over grpc-web with auth enabled.
func newConnectTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	grpcSrv := grpc.NewServer()
	grpcSrv.RegisterService(&echoServiceDesc, &echoWatchServer{})
	t.Cleanup(grpcSrv.Stop)

	handler := NewConnectHandler(grpcSrv, ConnectConfig{
		Token:          connectTestToken,
		AllowedOrigins: []string{connectTestOrigin},
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// grpcWebFrame wraps a message in the grpc-web length-prefixed DATA frame.
func grpcWebFrame(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	frame := make([]byte, 5, 5+len(body))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	return append(frame, body...)
}

func watchRequest(t *testing.T, ctx context.Context, url, token string) *http.Request {
	t.Helper()
	body := grpcWebFrame(t, &wrapperspb.StringValue{Value: "ws-1"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		url+"/test.v1.EchoService/Watch", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("X-Grpc-Web", "1")
	req.Header.Set("Origin", connectTestOrigin)
	if token != "" {
		req.Header.Set(ConnectTokenHeader, token)
	}
	return req
}

func TestConnectAuthRejectsMissingAndWrongToken(t *testing.T) {
	srv := newConnectTestServer(t)

	for name, token := range map[string]string{"missing": "", "wrong": "not-the-token"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resp, err := http.DefaultClient.Do(watchRequest(t, ctx, srv.URL, token))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if got := resp.Header.Get("grpc-status"); got != "16" {
				t.Fatalf("expected grpc-status 16 (UNAUTHENTICATED), got %q (http %d)", got, resp.StatusCode)
			}
		})
	}
}

func TestConnectAuthStreamsWithToken(t *testing.T) {
	srv := newConnectTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(watchRequest(t, ctx, srv.URL, connectTestToken))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("grpc-status"); got != "" && got != "0" {
		t.Fatalf("authenticated request failed with grpc-status %q", got)
	}

	// Read exactly the first streamed frame: the echoed snapshot.
	header := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if header[0] != 0x00 {
		t.Fatalf("expected a DATA frame, got flag 0x%02x", header[0])
	}
	payload := make([]byte, binary.BigEndian.Uint32(header[1:5]))
	if _, err := io.ReadFull(resp.Body, payload); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	var snap wrapperspb.StringValue
	if err := proto.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snap.GetValue() != "ws-1" {
		t.Fatalf("expected snapshot for ws-1, got %q", snap.GetValue())
	}
}

func TestConnectAuthPreflightHonoursOriginAllowlist(t *testing.T) {
	srv := newConnectTestServer(t)

	preflight := func(origin string) *http.Response {
		req, err := http.NewRequest(http.MethodOptions, srv.URL+"/test.v1.EchoService/Watch", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type,x-grpc-web,"+ConnectTokenHeader+",llmd-tenant-id,llmd-scope-id")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	allowed := preflight(connectTestOrigin)
	if got := allowed.Header.Get("Access-Control-Allow-Origin"); got != connectTestOrigin {
		t.Fatalf("expected the allowed origin echoed, got %q", got)
	}
	acah := strings.ToLower(allowed.Header.Get("Access-Control-Allow-Headers"))
	for _, want := range []string{"content-type", "x-grpc-web", ConnectTokenHeader, "llmd-tenant-id", "llmd-scope-id", "llmd-principal-id", "llmd-system-prompt-sections"} {
		if !strings.Contains(acah, want) {
			t.Fatalf("Access-Control-Allow-Headers %q is missing %q", acah, want)
		}
	}

	denied := preflight("http://evil.example")
	if got := denied.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin must not be echoed, got %q", got)
	}
	if got := denied.Header.Get("Access-Control-Allow-Headers"); got != "" {
		t.Fatalf("disallowed origin must not get Access-Control-Allow-Headers, got %q", got)
	}
}

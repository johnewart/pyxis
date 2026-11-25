package serve

import (
	"net/http"
	"sort"

	"github.com/johnewart/pyxis/grpcutf8"
	"github.com/johnewart/pyxis/internal/tracing"
	"google.golang.org/grpc"
)

// Server bundles the shared *grpc.Server used by llmd's three server groups —
// core (LLM/tool) services, graph/index services and workspace services — and
// the Connect/grpc-web listener exposing the same server to browser clients.
// Grouping the registration calls this way keeps main.go from reaching into
// internal/server directly for the interceptor/listener plumbing; the individual
// Register<Service>Server calls for each group still belong to their own
// packages (internal/server, graphserver, wsserver) and are simply invoked through
// the RegisterCore/RegisterGraph/RegisterWorkspace closures below.
type Server struct {
	GRPC *grpc.Server
}

// NewGRPCServer builds a *grpc.Server configured with llmd's standard debug
// interceptors and message-size limits, ready for service registration.
//
// Every message it sends is made valid UTF-8 first (grpcutf8): a string field
// that is not - a tool's raw output, a byte-cut string - otherwise fails the
// whole call, which for a streamed chat turn kills the turn.
func NewGRPCServer(opts ...grpc.ServerOption) *grpc.Server {
	base := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(64 * 1024 * 1024),
		grpc.MaxSendMsgSize(64 * 1024 * 1024),
	}
	// Incoming W3C trace context is extracted for every call so llmd's own
	// spans (LlmServer.Tracer) continue the caller's trace. The handler uses
	// a no-op provider: it propagates context without creating spans of its
	// own and reads no process-wide OTel global.
	base = append(base, tracing.ServerOption(nil))
	all := append(base, opts...)
	// Chained last so caller-supplied interceptors (e.g. auth) run first and
	// the debug interceptors can see the authenticated client id.
	all = append(all,
		grpc.ChainUnaryInterceptor(DebugUnaryInterceptor, grpcutf8.UnaryServerInterceptor),
		grpc.ChainStreamInterceptor(DebugStreamInterceptor, grpcutf8.StreamServerInterceptor),
	)
	return grpc.NewServer(all...)
}

// NewServer builds the shared grpc.Server (see NewGRPCServer).
func NewServer(opts ...grpc.ServerOption) *Server {
	return &Server{GRPC: NewGRPCServer(opts...)}
}

// RegisterCore registers the core LLM/tool services onto the shared
// grpc.Server. The callback does the actual Register<Service>Server calls;
// this method exists so the call site in main.go names which server group is
// being wired, and so all three groups land on the one shared server.
func (s *Server) RegisterCore(register func(*grpc.Server)) { register(s.GRPC) }

// RegisterGraph registers the graph/index services onto the shared grpc.Server.
func (s *Server) RegisterGraph(register func(*grpc.Server)) { register(s.GRPC) }

// RegisterWorkspace registers the workspace services onto the shared grpc.Server.
func (s *Server) RegisterWorkspace(register func(*grpc.Server)) { register(s.GRPC) }

// ConnectHandler wraps the shared grpc.Server with grpc-web framing for
// browser clients (see NewConnectHandler).
//
// Serves RPCs ONLY — no /ws/session-events route. Use BrowserSurface for a
// browser-facing listener; this method remains for hosts that genuinely
// want grpc-web without the session-events side channel, and warns at
// construction so the omission is never silent.
func (s *Server) ConnectHandler(cfg ConnectConfig) http.Handler {
	return NewConnectHandler(s.GRPC, cfg)
}

// ServiceRoute is one registered gRPC service and its method names, the unit
// main.go prints at startup so the listener's actual surface is visible
// without guessing. Methods are bare names ("ChatStream"); the wire path a
// grpc-web client posts to is "/<Service>/<Method>".
type ServiceRoute struct {
	Service string
	Methods []string
}

// ServiceRoutes reports every service/method registered on the shared
// grpc.Server, sorted for stable output.
//
// This exists because "which routes does this process actually serve?" was
// previously unanswerable from the logs: registration happens across three
// separate Register* groups (see RegisterCore/RegisterGraph/RegisterWorkspace),
// so a service that failed to register, or a binary older than the client
// calling it, looked identical at runtime to a network problem. Printing the
// inventory at startup turns "unimplemented method" into a one-line diff
// against what the server said it had.
func ServiceRoutes(s *grpc.Server) []ServiceRoute {
	info := s.GetServiceInfo()
	out := make([]ServiceRoute, 0, len(info))
	for name, svc := range info {
		methods := make([]string, 0, len(svc.Methods))
		for _, m := range svc.Methods {
			methods = append(methods, m.Name)
		}
		sort.Strings(methods)
		out = append(out, ServiceRoute{Service: name, Methods: methods})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

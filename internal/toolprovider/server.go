package toolprovider

import (
	"context"
	"io"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// logModule is the llmdlog `module` field for this service.
const logModule = "toolprovider"

// SharedSecretEnvVar is the environment variable the standalone llmd binary
// (cmd/llmd) reads the tool-provider secret from. This package never reads
// it: the secret reaches Server only as Server.SharedSecret, which embedding
// hosts set via llmdhost.Config.ToolProviderSecret.
const SharedSecretEnvVar = "LLMD_TOOL_PROVIDER_SHARED_SECRET"

// Server implements llmv1.ToolProviderServiceServer.
type Server struct {
	llmv1.UnimplementedToolProviderServiceServer
	Hub *Hub

	// SharedSecret is the secret every provider/executor stream must present.
	//
	// It is REQUIRED. Registering tools over this stream lets a peer put
	// callable tools in front of the model, so an empty secret is a
	// misconfiguration that refuses every registration, not a check to skip.
	SharedSecret string
}

func (s *Server) expectedSharedSecret() string {
	return strings.TrimSpace(s.SharedSecret)
}

// WarnIfSharedSecretMissing logs a warning when SharedSecret is empty.
// Composition roots call this at startup so the misconfiguration is visible
// before the first provider tries to connect and silently fails to register.
func (s *Server) WarnIfSharedSecretMissing() {
	if s.expectedSharedSecret() != "" {
		return
	}
	llmdlog.Stdout(logModule, llmdlog.LevelWarn,
		"no tool provider shared secret configured (llmdhost.Config.ToolProviderSecret; "+
			"%s for the standalone binary): remote tool provider/executor registration is "+
			"disabled and every attempt will be refused",
		SharedSecretEnvVar)
}

// ExecutorSecretMetadataKey is the gRPC metadata key an executor uses to
// present the shared secret (ExecutorHello carries no secret field).
const ExecutorSecretMetadataKey = "x-llmd-tool-provider-secret"

// authorizeProviderStream resolves the tenant scope and checks the shared
// secret for both hello kinds. kind names the registrant in logs.
func (s *Server) authorizeProviderStream(ctx context.Context, presentedSecret string, kind string) (serverutil.Scope, error) {
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"rejected %s registration: no tenant scope on stream: %v", kind, err)
		return serverutil.Scope{}, err
	}
	want := s.expectedSharedSecret()
	if want == "" {
		// Warn on EVERY attempt, not once: each rejected registration is a
		// provider whose tools silently never reach the model, and the operator
		// needs that correlated with the attempt rather than with startup only.
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"refused %s registration: no tool provider shared secret configured on this llmd: scope=%s",
			kind, sc.String())
		return serverutil.Scope{}, status.Error(codes.FailedPrecondition,
			"tool provider registration is disabled: no shared secret is configured on this llmd")
	}
	if strings.TrimSpace(presentedSecret) != want {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"rejected %s registration: invalid shared secret: scope=%s", kind, sc.String())
		return serverutil.Scope{}, status.Error(codes.PermissionDenied, "invalid tool provider shared secret")
	}
	return sc, nil
}

// serveExecutor handles a stream whose first frame was executor_hello.
func (s *Server) serveExecutor(stream llmv1.ToolProviderService_ConnectServer, hello *llmv1.ExecutorHello) error {
	var presented string
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		if v := md.Get(ExecutorSecretMetadataKey); len(v) > 0 {
			presented = v[0]
		}
	}
	sc, err := s.authorizeProviderStream(stream.Context(), presented, "executor")
	if err != nil {
		return err
	}
	ack, detach, superseded := s.Hub.handleExecutorHello(sc, hello, stream)
	defer detach()
	if err := stream.Send(&llmv1.ToolServerToProvider{
		Frame: &llmv1.ToolServerToProvider_ExecutorHelloAck{ExecutorHelloAck: ack},
	}); err != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"executor hello ack send failed: scope=%s executor_id=%s: %v", sc.String(), hello.GetExecutorId(), err)
		return err
	}

	type recvd struct {
		in  *llmv1.ToolProviderToServer
		err error
	}
	frames := make(chan recvd)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			in, err := stream.Recv()
			select {
			case frames <- recvd{in, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-superseded:
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"executor stream superseded by a newer instance; closing: scope=%s executor_id=%s", sc.String(), hello.GetExecutorId())
			return status.Error(codes.Aborted, "executor superseded by a newer instance")
		case r := <-frames:
			if r.err == io.EOF {
				return nil
			}
			if r.err != nil {
				return r.err
			}
			switch f := r.in.GetFrame().(type) {
			case *llmv1.ToolProviderToServer_ToolCallResult:
				if f.ToolCallResult == nil {
					llmdlog.Stdout(logModule, llmdlog.LevelWarn,
						"executor sent empty tool_call_result frame: scope=%s executor_id=%s", sc.String(), hello.GetExecutorId())
					continue
				}
				s.Hub.deliverCallResult(sc.TenantID, stream, f.ToolCallResult)
			default:
				llmdlog.Stdout(logModule, llmdlog.LevelDebug,
					"ignoring unhandled executor frame %T: scope=%s", r.in.GetFrame(), sc.String())
			}
		}
	}
}

// Connect accepts a bidirectional stream from a tool provider.
func (s *Server) Connect(stream llmv1.ToolProviderService_ConnectServer) error {
	if s == nil || s.Hub == nil {
		return status.Error(codes.Unavailable, "tool provider hub not configured")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if eh := first.GetExecutorHello(); eh != nil {
		return s.serveExecutor(stream, eh)
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument,
			"first frame must be tool_provider_hello or executor_hello")
	}
	sc, err := s.authorizeProviderStream(stream.Context(), hello.GetSharedSecret(), "tool provider")
	if err != nil {
		return err
	}

	unregister := s.Hub.Register(sc, stream)
	defer unregister()

	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch f := in.GetFrame().(type) {
		case *llmv1.ToolProviderToServer_Result:
			r := f.Result
			if r == nil {
				llmdlog.Stdout(logModule, llmdlog.LevelDebug,
					"ignoring tool provider result frame with nil result: scope=%s", sc.String())
				continue
			}
			s.Hub.DeliverResult(r.GetExecutionId(), r.GetToolId(), r.GetContent(), r.GetIsError(), r.GetDedupKey(), r.GetAttachments(), r.GetEndsTurn())
		default:
			llmdlog.Stdout(logModule, llmdlog.LevelDebug,
				"ignoring unhandled tool provider frame %T after hello: scope=%s", in.GetFrame(), sc.String())
		}
	}
}

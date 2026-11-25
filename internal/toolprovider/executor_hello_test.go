package toolprovider

import (
	"context"
	"net"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const testExecSecret = "s3cret"

func startExecServer(t *testing.T) (*Hub, llmv1.ToolProviderServiceClient) {
	t.Helper()
	return startExecServerSecret(t, testExecSecret)
}

// startExecServerSecret starts a server whose configured secret is secret
// ("" = none configured).
func startExecServerSecret(t *testing.T, secret string) (*Hub, llmv1.ToolProviderServiceClient) {
	t.Helper()
	// The secret must come from Server.SharedSecret alone; a stray env value
	// must not satisfy it.
	t.Setenv(SharedSecretEnvVar, "env-must-be-ignored")
	hub := NewHub()
	hub.InlineNames = func() map[string]struct{} { return map[string]struct{}{"read_file": {}} }
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	llmv1.RegisterToolProviderServiceServer(gs, &Server{Hub: hub, SharedSecret: secret})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return hub, llmv1.NewToolProviderServiceClient(conn)
}

// openExec opens a stream for tenant with secret, sends hello, and returns the
// stream plus the ack (nil ack with err when the server refused).
func openExec(t *testing.T, c llmv1.ToolProviderServiceClient, tenant, secret string, h *llmv1.ExecutorHello) (llmv1.ToolProviderService_ConnectClient, *llmv1.ExecutorHelloAck, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	ctx = tenantScope(tenant).OutgoingContext(ctx)
	if secret != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, ExecutorSecretMetadataKey, secret)
	}
	st, err := c.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := st.Send(&llmv1.ToolProviderToServer{Frame: &llmv1.ToolProviderToServer_ExecutorHello{ExecutorHello: h}}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	in, err := st.Recv()
	if err != nil {
		return st, nil, err
	}
	ack := in.GetExecutorHelloAck()
	if ack == nil {
		t.Fatalf("first server frame is not an ack: %T", in.GetFrame())
	}
	return st, ack, nil
}

func mustAck(t *testing.T, c llmv1.ToolProviderServiceClient, tenant string, h *llmv1.ExecutorHello) (llmv1.ToolProviderService_ConnectClient, *llmv1.ExecutorHelloAck) {
	t.Helper()
	st, ack, err := openExec(t, c, tenant, testExecSecret, h)
	if err != nil {
		t.Fatalf("hello refused: %v", err)
	}
	return st, ack
}

func TestExecutorHelloAcceptsNewAndRejectsInline(t *testing.T) {
	_, c := startExecServer(t)
	_, ack := mustAck(t, c, "t1", hello("ex", "i1", execDef("deploy", "ex"), execDef("read_file", "ex")))
	if !accepted(ack, "deploy") {
		t.Fatalf("deploy not accepted: %+v", ack)
	}
	if accepted(ack, "read_file") || rejectedReason(ack, "read_file") == "" {
		t.Fatalf("inline name must be rejected with a reason: %+v", ack)
	}
	if ack.GetMaxResultBytes() != MaxResultBytes {
		t.Fatalf("max_result_bytes=%d", ack.GetMaxResultBytes())
	}
}

func TestExecutorHelloSameTenantConflict(t *testing.T) {
	_, c := startExecServer(t)
	_, a1 := mustAck(t, c, "t1", hello("ex1", "i1", execDef("deploy", "ex1")))
	if !accepted(a1, "deploy") {
		t.Fatalf("first claim rejected: %+v", a1)
	}
	_, a2 := mustAck(t, c, "t1", hello("ex2", "i1", execDef("deploy", "ex2")))
	if accepted(a2, "deploy") || rejectedReason(a2, "deploy") == "" {
		t.Fatalf("same-tenant conflict not rejected: %+v", a2)
	}
}

func TestExecutorHelloCrossTenantClaimAccepted(t *testing.T) {
	_, c := startExecServer(t)
	mustAck(t, c, "t1", hello("ex1", "i1", execDef("deploy", "ex1")))
	_, a2 := mustAck(t, c, "t2", hello("ex2", "i1", execDef("deploy", "ex2")))
	if !accepted(a2, "deploy") {
		t.Fatalf("cross-tenant claim rejected: %+v", a2)
	}
}

func TestExecutorHelloSupersede(t *testing.T) {
	_, c := startExecServer(t)
	old, _ := mustAck(t, c, "t1", hello("ex", "i1", execDef("deploy", "ex")))
	_, a2 := mustAck(t, c, "t1", hello("ex", "i2", execDef("deploy", "ex")))
	if !accepted(a2, "deploy") {
		t.Fatalf("new instance not accepted: %+v", a2)
	}
	errc := make(chan error, 1)
	go func() { _, err := old.Recv(); errc <- err }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("old stream got a frame instead of ending")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old stream did not end after supersede")
	}
}

func TestExecutorHelloSecretRefusal(t *testing.T) {
	_, c := startExecServer(t)
	_, ack, err := openExec(t, c, "t1", "wrong", hello("ex", "i1", execDef("deploy", "ex")))
	if ack != nil || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong secret: ack=%v err=%v", ack, err)
	}
	// Presenting the env value must not work: the server reads only its field.
	_, ack, err = openExec(t, c, "t1", "env-must-be-ignored", hello("ex", "i1", execDef("deploy", "ex")))
	if ack != nil || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("env secret accepted: ack=%v err=%v", ack, err)
	}
	_, unset := startExecServerSecret(t, "")
	_, ack, err = openExec(t, unset, "t1", testExecSecret, hello("ex", "i1", execDef("deploy", "ex")))
	if ack != nil || status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unset secret: ack=%v err=%v", ack, err)
	}
}

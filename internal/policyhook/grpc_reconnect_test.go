package policyhook

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCClientRefreshesOnReconnect(t *testing.T) {
	f := &fakeServer{}
	var current atomic.Pointer[bufconn.Listener]
	start := func() *grpc.Server {
		lis := bufconn.Listen(1 << 20)
		srv := grpc.NewServer()
		llmv1.RegisterPolicyHookServiceServer(srv, f)
		current.Store(lis)
		go func() { _ = srv.Serve(lis) }()
		return srv
	}
	srv := start()
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return current.Load().DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewGRPCClient(conn, time.Second)
	c.WatchReconnects(ctx, conn)

	if _, err := c.GetCheckPolicy(ctx); err != nil {
		t.Fatalf("GetCheckPolicy: %v", err)
	}
	if n := f.policyCalls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}

	srv.Stop()
	srv = start()

	deadline := time.Now().Add(10 * time.Second)
	for f.policyCalls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("no second GetCheckPolicy after reconnect; calls = %d", f.policyCalls.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

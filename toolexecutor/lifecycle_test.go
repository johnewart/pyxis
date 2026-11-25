package toolexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolseam"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const lcSecret = "lc-secret"

type seenHello struct {
	tenant string
	hello  *llmv1.ExecutorHello
}

// streamRecorder observes executor streams on the real server: it records
// each hello with the tenant it arrived under and can tear down live streams.
type streamRecorder struct {
	connects atomic.Int32
	hellos   chan seenHello
	mu       sync.Mutex
	kill     chan struct{}
}

func (r *streamRecorder) killAll() {
	r.mu.Lock()
	close(r.kill)
	r.kill = make(chan struct{})
	r.mu.Unlock()
}

type recStream struct {
	grpc.ServerStream
	rec  *streamRecorder
	kill chan struct{}
}

func (s *recStream) RecvMsg(m any) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.ServerStream.RecvMsg(m) }()
	select {
	case <-s.kill:
		return errors.New("test: stream torn down")
	case err := <-errCh:
		if err == nil {
			if f, ok := m.(*llmv1.ToolProviderToServer); ok && f.GetExecutorHello() != nil {
				sc, _ := tenantscope.FromContext(s.Context())
				s.rec.hellos <- seenHello{tenant: sc.TenantID, hello: f.GetExecutorHello()}
			}
		}
		return err
	}
}

func startHub(t *testing.T, inline ...string) (*toolprovider.Hub, llmv1.ToolProviderServiceClient, *streamRecorder) {
	t.Helper()
	hub := toolprovider.NewHub()
	hub.InlineNames = func() map[string]struct{} {
		out := map[string]struct{}{}
		for _, n := range inline {
			out[n] = struct{}{}
		}
		return out
	}
	rec := &streamRecorder{hellos: make(chan seenHello, 32), kill: make(chan struct{})}
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		rec.connects.Add(1)
		rec.mu.Lock()
		k := rec.kill
		rec.mu.Unlock()
		return h(srv, &recStream{ServerStream: ss, rec: rec, kill: k})
	}))
	llmv1.RegisterToolProviderServiceServer(gs, &toolprovider.Server{Hub: hub, SharedSecret: lcSecret})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return hub, llmv1.NewToolProviderServiceClient(conn), rec
}

type lcClient struct {
	c    *Client
	logs *logCapture
	acks chan *llmv1.ExecutorHelloAck
	done chan error
	stop context.CancelFunc
}

func runClient(t *testing.T, svc llmv1.ToolProviderServiceClient, secret string, tenants []tenantscope.Scope, tools ...string) *lcClient {
	t.Helper()
	h := toolseam.NewHandlers()
	var defs []*llmv1.ToolDefinition
	for _, n := range tools {
		if err := h.Register(n, func(context.Context, json.RawMessage, toolseam.Invocation) (toolseam.Result, error) {
			return toolseam.Result{}, nil
		}); err != nil {
			t.Fatal(err)
		}
		defs = append(defs, &llmv1.ToolDefinition{Name: n})
	}
	lc := &lcClient{logs: &logCapture{}, acks: make(chan *llmv1.ExecutorHelloAck, 32), done: make(chan error, 1)}
	c, err := New(Options{
		ExecutorID: "exec-lc", Handlers: h, Definitions: defs, Allowlist: tools, Logger: lc.logs.log,
		SharedSecret: secret, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond,
		OnAck: func(_ tenantscope.Scope, a *llmv1.ExecutorHelloAck) { lc.acks <- a },
	})
	if err != nil {
		t.Fatal(err)
	}
	lc.c = c
	ctx, cancel := context.WithCancel(context.Background())
	lc.stop = cancel
	go func() { lc.done <- c.Run(ctx, svc, tenants) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-lc.done:
		case <-time.After(2 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	return lc
}

func nextHello(t *testing.T, rec *streamRecorder) seenHello {
	t.Helper()
	select {
	case h := <-rec.hellos:
		return h
	case <-time.After(3 * time.Second):
		t.Fatal("no ExecutorHello reached the hub")
		return seenHello{}
	}
}

func nextAck(t *testing.T, lc *lcClient) *llmv1.ExecutorHelloAck {
	t.Helper()
	select {
	case a := <-lc.acks:
		return a
	case <-time.After(3 * time.Second):
		t.Fatal("no ack")
		return nil
	}
}

func hasAccepted(a *llmv1.ExecutorHelloAck, name string) bool {
	for _, n := range a.GetAcceptedTools() {
		if n == name {
			return true
		}
	}
	return false
}

func hasDef(hub *toolprovider.Hub, tenant, name string) bool {
	for _, d := range hub.ExecutorDefinitions(tenant) {
		if d.GetName() == name {
			return true
		}
	}
	return false
}

func TestLifecycleOneStreamPerTenant(t *testing.T) {
	hub, svc, rec := startHub(t)
	tenants := []tenantscope.Scope{{TenantID: "t1", ScopeID: "s"}, {TenantID: "t2", ScopeID: "s"}}
	lc := runClient(t, svc, lcSecret, tenants, "deploy")
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		h := nextHello(t, rec)
		if h.hello.GetInstanceId() == "" || h.hello.GetInstanceId() != lc.c.InstanceID() {
			t.Fatalf("instance_id=%q want %q", h.hello.GetInstanceId(), lc.c.InstanceID())
		}
		seen[h.tenant] = true
	}
	if !seen["t1"] || !seen["t2"] {
		t.Fatalf("hellos arrived under tenants %v", seen)
	}
	for i := 0; i < 2; i++ {
		if a := nextAck(t, lc); !hasAccepted(a, "deploy") {
			t.Fatalf("deploy not accepted: %+v", a)
		}
	}
	if !hasDef(hub, "t1", "deploy") || !hasDef(hub, "t2", "deploy") {
		t.Fatal("deploy not registered under each configured tenant")
	}
	if hasDef(hub, "t3", "deploy") {
		t.Fatal("deploy leaked into an unconfigured tenant")
	}
}

func TestRejectedInlineNameReported(t *testing.T) {
	_, svc, _ := startHub(t, "read_file")
	lc := runClient(t, svc, lcSecret, []tenantscope.Scope{{TenantID: "t1", ScopeID: "s"}}, "deploy", "read_file")
	a := nextAck(t, lc)
	var hubReason string
	for _, r := range a.GetRejectedTools() {
		if r.GetName() == "read_file" {
			hubReason = r.GetReason()
		}
	}
	if hubReason == "" {
		t.Fatalf("hub did not reject inline name: %+v", a)
	}
	var got *Rejection
	for _, r := range lc.c.Rejections() {
		if r.Name == "read_file" {
			r := r
			got = &r
		}
	}
	if got == nil || got.Reason != hubReason || got.TenantID != "t1" {
		t.Fatalf("client rejection=%+v want reason %q", got, hubReason)
	}
	if !lc.logs.has(string(llmdlog.LevelWarn) + " hub rejected tool: tenant=t1 executor_id=exec-lc name=read_file") {
		t.Fatalf("rejection not logged at warn: %v", lc.logs.lines)
	}
}

func TestReconnectReusesInstanceID(t *testing.T) {
	hub, svc, rec := startHub(t)
	lc := runClient(t, svc, lcSecret, []tenantscope.Scope{{TenantID: "t1", ScopeID: "s"}}, "deploy")
	h1 := nextHello(t, rec)
	nextAck(t, lc)
	rec.killAll()
	h2 := nextHello(t, rec)
	if h2.hello.GetInstanceId() != h1.hello.GetInstanceId() || h2.hello.GetInstanceId() != lc.c.InstanceID() {
		t.Fatalf("reconnect instance_id %q != %q", h2.hello.GetInstanceId(), h1.hello.GetInstanceId())
	}
	if a := nextAck(t, lc); !hasAccepted(a, "deploy") {
		t.Fatalf("hub did not re-accept same instance: %+v", a)
	}
	if !hasDef(hub, "t1", "deploy") {
		t.Fatal("deploy not owned after reconnect")
	}
	if !lc.logs.has("reconnecting") {
		t.Fatal("stream end not logged")
	}
}

func TestReconnectStopsOnCancel(t *testing.T) {
	_, svc, rec := startHub(t)
	// Wrong secret: every attempt is refused, so the loop keeps retrying.
	lc := runClient(t, svc, "wrong", []tenantscope.Scope{{TenantID: "t1", ScopeID: "s"}}, "deploy")
	deadline := time.Now().Add(3 * time.Second)
	for rec.connects.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d connect attempts", rec.connects.Load())
		}
		time.Sleep(time.Millisecond)
	}
	lc.stop()
	select {
	case <-lc.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancel")
	}
	lc.done <- nil // satisfy cleanup
	n := rec.connects.Load()
	time.Sleep(50 * time.Millisecond)
	if m := rec.connects.Load(); m != n {
		t.Fatalf("connect attempts continued after cancel: %d -> %d", n, m)
	}
}

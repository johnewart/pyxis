package toolprovider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/grpc"
)

type stubConnectServer struct {
	grpc.ServerStream
	mu   sync.Mutex
	sent []*llmv1.ToolServerToProvider
	ch   chan *llmv1.ToolServerToProvider
}

func newStub() *stubConnectServer {
	return &stubConnectServer{ch: make(chan *llmv1.ToolServerToProvider, 8)}
}

func (s *stubConnectServer) Send(f *llmv1.ToolServerToProvider) error {
	s.mu.Lock()
	s.sent = append(s.sent, f)
	s.mu.Unlock()
	s.ch <- f
	return nil
}

func (s *stubConnectServer) Recv() (*llmv1.ToolProviderToServer, error) { select {} }
func (s *stubConnectServer) Context() context.Context                   { return context.Background() }

type invokeOut struct {
	content string
	err     error
}

func startInvoke(h *Hub, sc serverutil.Scope) <-chan invokeOut {
	out := make(chan invokeOut, 1)
	go func() {
		c, _, _, _, err := h.Invoke(context.Background(), sc, "a", "s", "", "", "tool", "{}", 60*time.Second)
		out <- invokeOut{c, err}
	}()
	return out
}

func waitSent(t *testing.T, s *stubConnectServer) *llmv1.ToolServerToProvider {
	t.Helper()
	select {
	case f := <-s.ch:
		return f
	case <-time.After(time.Second):
		t.Fatal("invoke frame never sent")
		return nil
	}
}

func waitOut(t *testing.T, out <-chan invokeOut) invokeOut {
	t.Helper()
	select {
	case o := <-out:
		return o
	case <-time.After(time.Second):
		t.Fatal("Invoke did not return within 1s")
		return invokeOut{}
	}
}

var testScope = serverutil.Scope{TenantID: "t1", ScopeID: "s1"}

func TestHubDisconnect_UnregisterFailsPending(t *testing.T) {
	h := NewHub()
	s := newStub()
	unregister := h.Register(testScope, s)
	out := startInvoke(h, testScope)
	waitSent(t, s)
	unregister()
	o := waitOut(t, out)
	if !errors.Is(o.err, ErrProviderDisconnected) {
		t.Fatalf("want ErrProviderDisconnected, got %v", o.err)
	}
}

func TestHubDisconnect_SupersedeFailsPending(t *testing.T) {
	h := NewHub()
	s1 := newStub()
	unregister1 := h.Register(testScope, s1)
	out := startInvoke(h, testScope)
	waitSent(t, s1)
	unregister2 := h.Register(testScope, newStub())
	defer unregister2()
	o := waitOut(t, out)
	if !errors.Is(o.err, ErrProviderDisconnected) {
		t.Fatalf("want ErrProviderDisconnected, got %v", o.err)
	}
	unregister1() // stale unregister must not remove the new stream
	if !h.HasProvider(testScope) {
		t.Fatal("superseding stream was removed by stale unregister")
	}
}

func TestHubDisconnect_DeliverResultStillCompletes(t *testing.T) {
	h := NewHub()
	s := newStub()
	unregister := h.Register(testScope, s)
	out := startInvoke(h, testScope)
	f := waitSent(t, s)
	id := f.GetInvoke().GetExecutionId()
	if !h.DeliverResult(id, "tool", "hello", false, "", nil, false) {
		t.Fatal("DeliverResult returned false")
	}
	o := waitOut(t, out)
	if o.err != nil || o.content != "hello" {
		t.Fatalf("got content=%q err=%v", o.content, o.err)
	}
	unregister()
	if h.DeliverResult(id, "tool", "late", false, "", nil, false) {
		t.Fatal("late DeliverResult should return false")
	}
}

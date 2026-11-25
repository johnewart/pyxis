package tooldelegate

import (
	"errors"
	"testing"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
)

func TestRegistry_Submit_and_Abort(t *testing.T) {
	r := NewRegistry()
	sc := serverutil.Scope{TenantID: "tenant"}
	ch := r.Register("n1", "agent", "tc1", sc)
	go func() {
		time.Sleep(5 * time.Millisecond)
		_ = r.Submit("n1", "agent", "tc1", sc, "ok", false, false)
	}()
	select {
	case res := <-ch:
		if res.Content != "ok" || res.IsError {
			t.Fatalf("res=%+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting result")
	}
}

func TestRegistry_Submit_scopeMismatch(t *testing.T) {
	r := NewRegistry()
	r.Register("n3", "a", "t", serverutil.Scope{TenantID: "t1", ScopeID: "s1"})
	err := r.Submit("n3", "a", "t", serverutil.Scope{TenantID: "t1", ScopeID: "s2"}, "x", false, false)
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("err=%v, want ErrScopeMismatch", err)
	}
}

func TestRegistry_Abort_unblocks(t *testing.T) {
	r := NewRegistry()
	ch := r.Register("n2", "a", "t", serverutil.Scope{TenantID: "u"})
	go func() {
		time.Sleep(5 * time.Millisecond)
		r.Abort("n2")
	}()
	_, ok := <-ch
	if ok {
		t.Fatal("expected closed channel")
	}
}

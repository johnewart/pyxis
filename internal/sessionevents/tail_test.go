package sessionevents

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
)

var testScope = serverutil.Scope{TenantID: "tenant-tail", ScopeID: "scope-tail"}

const testSession = "sess-tail"

// countingLog counts table reads so a test can prove the tail does not poll.
type countingLog struct {
	*store.SessionEventRepo
	reads atomic.Int64
}

func (c *countingLog) GetEventsSince(ctx context.Context, sc serverutil.Scope, sid string, after int64) ([]store.SessionEventRow, error) {
	c.reads.Add(1)
	return c.SessionEventRepo.GetEventsSince(ctx, sc, sid, after)
}

func appendDelta(t *testing.T, repo *store.SessionEventRepo, s string) {
	t.Helper()
	b, err := protojson.Marshal(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
		AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: "t", Delta: s},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendEvent(context.Background(), testScope, testSession, string(b)); err != nil {
		t.Fatal(err)
	}
}

func startTail(t *testing.T, log Log, send func(*llmv1.ConversationStreamFrame) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Tail(ctx, log, testScope, testSession, 0, send, "test")
	}()
	t.Cleanup(func() { cancel(); <-done })
}

// An idle tail costs one replay read and then nothing: no fallback polling.
// Live rows arrive by push, still without reading the table.
func TestTail_IdleDoesNotQuery(t *testing.T) {
	repo := store.NewSessionEventRepo(store.NewTestSQLite(t))
	log := &countingLog{SessionEventRepo: repo}
	got := make(chan string, 8)
	startTail(t, log, func(f *llmv1.ConversationStreamFrame) error {
		got <- f.GetAssistantTextDelta().GetDelta()
		return nil
	})

	time.Sleep(100 * time.Millisecond)
	appendDelta(t, repo, "live")
	select {
	case d := <-got:
		if d != "live" {
			t.Fatalf("got %q, want live", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("live row not delivered")
	}
	time.Sleep(100 * time.Millisecond)
	if n := log.reads.Load(); n != 1 {
		t.Fatalf("table reads = %d, want exactly 1 (the replay)", n)
	}
}

// A subscriber far slower than the producer overflows its live buffer; the
// dropped rows are recovered from the log, and every frame still arrives
// exactly once, in order.
func TestTail_OverflowRecoversFromLog_NoGapNoDup(t *testing.T) {
	prev := LiveBuffer
	LiveBuffer = 2
	t.Cleanup(func() { LiveBuffer = prev })

	repo := store.NewSessionEventRepo(store.NewTestSQLite(t))
	appendDelta(t, repo, "d0") // replayed, not live

	const total = 200
	got := make(chan string, total+1)
	release := make(chan struct{})
	startTail(t, repo, func(f *llmv1.ConversationStreamFrame) error {
		<-release // stall the first send so the burst overflows the buffer
		got <- f.GetAssistantTextDelta().GetDelta()
		return nil
	})
	time.Sleep(50 * time.Millisecond)
	for i := 1; i < total; i++ {
		appendDelta(t, repo, fmt.Sprintf("d%d", i))
	}
	close(release)

	timeout := time.After(10 * time.Second)
	for i := 0; i < total; i++ {
		select {
		case d := <-got:
			if want := fmt.Sprintf("d%d", i); d != want {
				t.Fatalf("frame %d: got %q, want %q (gap or duplicate)", i, d, want)
			}
		case <-timeout:
			t.Fatalf("timed out after %d of %d frames", i, total)
		}
	}
	select {
	case d := <-got:
		t.Fatalf("unexpected extra frame %q", d)
	case <-time.After(200 * time.Millisecond):
	}
}

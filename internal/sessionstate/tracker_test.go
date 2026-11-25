package sessionstate

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
)

func frameJSON(t *testing.T, f *llmv1.ConversationStreamFrame) string {
	t.Helper()
	b, err := protojson.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFold(t *testing.T) {
	s := State{SessionID: "s"}
	steps := []struct {
		frame *llmv1.ConversationStreamFrame
		want  State
	}{
		{&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{}}},
			State{SessionID: "s", Running: true}},
		{&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_ToolExecutionStarted{ToolExecutionStarted: &llmv1.EvToolExecutionStarted{Name: "read_code"}}},
			State{SessionID: "s", Running: true, ActiveTool: "read_code"}},
		{&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_SessionStatus{SessionStatus: &llmv1.EvSessionStatus{Headline: "Reading"}}},
			State{SessionID: "s", Running: true, ActiveTool: "read_code", StatusHeadline: "Reading"}},
		{&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: &llmv1.EvToolExecutionFinished{}}},
			State{SessionID: "s", Running: true, StatusHeadline: "Reading"}},
		{&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFailed{AssistantTurnFailed: &llmv1.EvAssistantTurnFailed{}}},
			State{SessionID: "s", StatusHeadline: "Reading"}},
	}
	for i, st := range steps {
		next, _ := Fold(s, st.frame)
		if next != st.want {
			t.Fatalf("step %d: got %+v want %+v", i, next, st.want)
		}
		s = next
	}
	if _, changed := Fold(s, &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{}}}); changed {
		t.Fatal("a text delta must not change the summary")
	}
}

// A subscriber that falls behind is told to resync rather than blocking the
// appender; the snapshot then holds the latest state.
func TestTracker_LagSignalsResync(t *testing.T) {
	old := SubscriberBuffer
	SubscriberBuffer = 1
	t.Cleanup(func() { SubscriberBuffer = old })

	repo := store.NewSessionEventRepo(store.NewTestSQLite(t))
	tr := New(repo)
	t.Cleanup(tr.Stop)
	sc := serverutil.Scope{TenantID: "t", ScopeID: "p"}
	sub := tr.Subscribe(sc)
	defer sub.Close()

	start := frameJSON(t, &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{}}})
	for _, sid := range []string{"a", "b", "c"} {
		if _, err := repo.AppendEvent(context.Background(), sc, sid, start); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-sub.Lagged:
	default:
		t.Fatal("expected a lag signal after overflowing the buffer")
	}
	if got := len(tr.Snapshot(sc)); got != 3 {
		t.Fatalf("snapshot has %d sessions, want 3", got)
	}
	if got := len(tr.Snapshot(serverutil.Scope{TenantID: "t", ScopeID: "other"})); got != 0 {
		t.Fatalf("other scope snapshot has %d sessions, want 0", got)
	}
}

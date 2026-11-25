package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"github.com/johnewart/pyxis/toolseam"
	"github.com/johnewart/pyxis/visualcards"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// cardsBackend calls show_budget on the first round and records what the
// model is sent on the second, so the test can check the card's data never
// reaches it.
type cardsBackend struct {
	stubBackend
	rounds      int
	secondRound []*llmv1.Message
}

func (b *cardsBackend) ChatStream(_ context.Context, msgs []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.rounds++
	const turnID = "turn_cards"
	reply := &llmv1.AssistantReply{Id: "asst_done", Content: "Here is your budget.", FinishReason: "end_turn"}
	if b.rounds == 1 {
		reply = &llmv1.AssistantReply{Id: "asst_tools", ToolCalls: []*llmv1.ToolCall{{Id: "call_budget", Name: "show_budget", ArgumentsJson: `{}`}}}
	} else {
		b.secondRound = msgs
	}
	for _, f := range []*llmv1.ConversationStreamFrame{
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}}},
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID}}},
		{Frame: &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply}},
	} {
		f.SchemaVersion = conversationevents.SchemaVersion
		if err := emit(f); err != nil {
			return err
		}
	}
	return nil
}

type cardsRun struct {
	backend  *cardsBackend
	upserted []*llmv1.EvAttachmentUpserted
	finished *llmv1.EvToolExecutionFinished
	repo     *store.ChatAttachmentRepo
	scope    serverutil.Scope
}

// runCardsChat is a plain llmd host (no embedding host, no host session client)
// whose one Go tool returns a chart card built with llmd/visualcards.
func runCardsChat(t *testing.T, withStore bool) cardsRun {
	t.Helper()
	const ws = "ws-cards"
	sc := serverutil.Scope{TenantID: ws}
	ctx := sc.OutgoingContext(context.Background())

	be := &cardsBackend{}
	reg, err := providers.NewRegistryFromEntries("stub_cards", map[string]*providers.Entry{
		"stub_cards": {ID: "stub_cards", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "s_cards", LlmProviderId: strPtr("stub_cards")}, sc); err != nil {
		t.Fatal(err)
	}
	repo := store.NewChatAttachmentRepo(db)
	llmSrv := &LlmServer{Providers: providers.NewComposite(reg, nil), Tools: toolregistry.New(), SessionRuntimes: mgr}
	if withStore {
		llmSrv.Attachments = repo
	}
	showBudget := registerInlineBuiltin("show_budget", func(context.Context, json.RawMessage, toolexec.Invocation) (toolexec.Result, error) {
		card, err := visualcards.NewChartCard("budget-1", "", map[string]any{
			"title": "October spending",
			"mark":  "bar",
			"data":  map[string]any{"values": []any{map[string]any{"category": "SECRET-RENT", "amount": 1800}}},
		})
		if err != nil {
			return toolexec.Result{}, err
		}
		payload, err := card.PayloadJSON()
		if err != nil {
			return toolexec.Result{}, err
		}
		return toolexec.Result{
			Content: `{"shown":"` + card.Summary() + `"}`,
			Cards:   []toolseam.Card{{ID: card.ID, Kind: visualcards.Kind, PayloadJSON: payload}},
		}, nil
	})
	if err := showBudget(llmSrv.Tools, llmSrv.handlerSet()); err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, llmSrv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	st, err := llmv1.NewLlmServiceClient(conn).ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "show me my budget", Id: "u1"}},
		SessionId: strPtr("s_cards"),
	})
	if err != nil {
		t.Fatal(err)
	}
	run := cardsRun{backend: be, repo: repo, scope: sc}
	for {
		f, err := st.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if up := f.GetAttachmentUpserted(); up != nil {
			run.upserted = append(run.upserted, up)
		}
		if fin := f.GetToolExecutionFinished(); fin != nil {
			run.finished = fin
		}
	}
	return run
}

// A Go tool returns a chart card in its Result and does nothing else: llmd
// stores it anchored to the call, streams it live, and the model only ever
// sees the tool's one-line summary.
func TestToolReturnedCardIsShownAndNeverSentToModel(t *testing.T) {
	run := runCardsChat(t, true)
	if run.finished == nil || !run.finished.GetSuccess() {
		t.Fatalf("tool did not succeed: %+v", run.finished)
	}
	if len(run.upserted) != 1 {
		t.Fatalf("live attachment frames = %d, want 1", len(run.upserted))
	}
	up := run.upserted[0]
	if up.GetKind() != visualcards.Kind || up.GetAttachmentId() != "budget-1" || up.GetAfterToolCallId() != "call_budget" {
		t.Errorf("frame = %+v", up)
	}
	rec, err := run.repo.Find(context.Background(), run.scope, "s_cards", "budget-1")
	if err != nil || rec == nil {
		t.Fatalf("card not stored: %v", err)
	}
	if rec.AfterToolCallID != "call_budget" || !strings.Contains(rec.PayloadJSON, `"type":"chart"`) {
		t.Errorf("stored = %+v", rec)
	}
	if run.backend.rounds != 2 {
		t.Fatalf("model rounds = %d, want 2", run.backend.rounds)
	}
	var sawSummary bool
	for _, m := range run.backend.secondRound {
		if strings.Contains(m.GetContent(), "SECRET-RENT") {
			t.Fatalf("card data reached the model in a %s message", m.GetRole())
		}
		if strings.Contains(m.GetContent(), `October spending`) {
			sawSummary = true
		}
	}
	if !sawSummary {
		t.Error("the model never saw the tool's summary of what was shown")
	}
}

// If the card cannot be shown, the call fails: the model must not be told
// the user saw something they did not.
func TestToolReturnedCardThatCannotBeShownFailsTheCall(t *testing.T) {
	run := runCardsChat(t, false)
	if run.finished == nil || run.finished.GetSuccess() {
		t.Fatalf("tool reported success with no attachment store: %+v", run.finished)
	}
	if len(run.upserted) != 0 {
		t.Fatalf("frames announced for a card that was not stored: %+v", run.upserted)
	}
	var reported bool
	for _, m := range run.backend.secondRound {
		if strings.Contains(m.GetContent(), "could not be shown to the user") {
			reported = true
		}
	}
	if !reported {
		t.Error("the model was not told the result could not be shown")
	}
}

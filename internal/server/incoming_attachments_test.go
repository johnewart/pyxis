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
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// recordingTextBackend replies with plain text and records the prompt of every
// call so a test can assert exactly what the model was sent.
type recordingTextBackend struct {
	stubBackend
	prompts [][]*llmv1.Message
}

func (b *recordingTextBackend) ChatStream(_ context.Context, msgs []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.prompts = append(b.prompts, msgs)
	const turnID = "turn_rec"
	reply := &llmv1.AssistantReply{Id: "asst_rec_" + string(rune('a'+len(b.prompts))), Content: "ok", FinishReason: "end_turn"}
	for _, f := range []*llmv1.ConversationStreamFrame{
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}}},
		{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: reply.Content}}},
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

// countIn reports how many times needle appears across the prompt, and the ids
// of the messages it appears in.
func countIn(prompt []*llmv1.Message, needle string) (int, []string) {
	n := 0
	var ids []string
	for _, m := range prompt {
		if c := strings.Count(m.GetContent(), needle); c > 0 {
			n += c
			ids = append(ids, m.GetId())
		}
	}
	return n, ids
}

// TestChatStream_IncomingUserMessageAttachments covers attachments a client
// sends ON a user message: the model sees their content on that message in the
// same send, they are persisted anchored to that user message, and later sends
// re-inject them exactly once (not duplicated, not moved to the newer message).
func TestChatStream_IncomingUserMessageAttachments(t *testing.T) {
	const ws = "ws-incoming-attachments"
	const sid = "agent_incoming_attachments"
	const marker = "ATTACHED-CONTEXT-7f3a"
	ctx := serverutil.Scope{TenantID: ws}.OutgoingContext(context.Background())

	be := &recordingTextBackend{}
	reg, err := providers.NewRegistryFromEntries("stub_rec", map[string]*providers.Entry{
		"stub_rec": {ID: "stub_rec", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sid,
		LlmProviderId: strPtr("stub_rec"),
	}, serverutil.Scope{TenantID: ws}); err != nil {
		t.Fatal(err)
	}
	attachmentRepo := store.NewChatAttachmentRepo(db)
	llmSrv := &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		Attachments:     attachmentRepo,
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
	client := llmv1.NewLlmServiceClient(conn)

	send := func(msg *llmv1.Message) []*llmv1.EvAttachmentUpserted {
		t.Helper()
		st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{Messages: []*llmv1.Message{msg}, SessionId: strPtr(sid)})
		if err != nil {
			t.Fatal(err)
		}
		var ups []*llmv1.EvAttachmentUpserted
		for {
			f, err := st.Recv()
			if err == io.EOF {
				return ups
			}
			if err != nil {
				t.Fatal(err)
			}
			if up := f.GetAttachmentUpserted(); up != nil {
				ups = append(ups, up)
			}
		}
	}

	ups := send(&llmv1.Message{
		Role: "user", Id: "u1", Content: "please look at this",
		Attachments: []*llmv1.MessageAttachment{{Id: "att1", Kind: "composer_text", PayloadJson: `{"name":"notes"}`, Content: marker}},
	})

	// Same send: the model sees the content, once, on u1.
	if len(be.prompts) != 1 {
		t.Fatalf("backend called %d times, want 1", len(be.prompts))
	}
	if n, ids := countIn(be.prompts[0], marker); n != 1 || len(ids) != 1 || ids[0] != "u1" {
		t.Fatalf("first send: marker count=%d in messages %v, want 1 in [u1]", n, ids)
	}
	// The upsert is announced to OTHER windows via the hub/event log, not echoed
	// on the sender's own stream (it already has the attachment it sent).
	_ = ups

	// Persisted, anchored to u1.
	rows, err := attachmentRepo.LoadRows(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("persisted %d attachment rows, want 1", len(rows))
	}
	var row struct {
		ID              string `json:"id"`
		AnchorMessageID string `json:"anchor_message_id"`
		Content         string `json:"content"`
	}
	if err := json.Unmarshal([]byte(rows[0].Content), &row); err != nil {
		t.Fatal(err)
	}
	if row.ID != "att1" || row.AnchorMessageID != "u1" || row.Content != marker {
		t.Fatalf("persisted row = %+v, want att1 anchored to u1 with content", row)
	}

	// Later send: re-injected exactly once, still on u1.
	send(&llmv1.Message{Role: "user", Id: "u2", Content: "and now?"})
	if len(be.prompts) != 2 {
		t.Fatalf("backend called %d times, want 2", len(be.prompts))
	}
	if n, ids := countIn(be.prompts[1], marker); n != 1 || len(ids) != 1 || ids[0] != "u1" {
		t.Fatalf("second send: marker count=%d in messages %v, want 1 in [u1]", n, ids)
	}
}

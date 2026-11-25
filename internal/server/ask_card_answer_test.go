package server

import (
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func choiceAnswer(cardID string, selected ...string) string {
	q := make([]string, len(selected))
	for i, s := range selected {
		q[i] = `"` + s + `"`
	}
	return `{"cardId":"` + cardID + `","type":"choice","choice":{"selected":[` + strings.Join(q, ",") + `]}}`
}

func TestAnswerAskCard_queuesFrontThenUpdatesQueuedThenUpdatesSent(t *testing.T) {
	e := newDrainEnv(t)
	attRepo := store.NewChatAttachmentRepo(e.db)
	e.impl.Attachments = attRepo

	// An agent card, as ask_user persists it (anchored to + after its tool call).
	if err := attRepo.Upsert(e.ctx, testScope, drainSessionID, "call_1", store.AttachmentInput{
		ID: "card1", Kind: "ask_card", AfterToolCallID: "call_1",
		PayloadJSON: `{"id":"card1","title":"Pick DB","text":"","type":"choice","allowAdditionalText":false,"choice":{"choices":["Postgres","MySQL"],"allowMultiple":false},"status":"pending"}`,
	}); err != nil {
		t.Fatal(err)
	}

	e.setPaused(t, true)
	e.enqueue(t, "something I queued earlier")

	res, err := e.impl.AnswerAskCard(e.ctx, &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "card1", ResponseJson: choiceAnswer("card1", "MySQL")})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetUpdatedExisting() || res.GetEntry() == nil {
		t.Fatalf("first answer should queue a new entry, got %+v", res)
	}
	if !res.GetEntry().GetTurnOptions().GetDisableTools() {
		t.Fatal("queued ask-card answer should run with tools disabled by default (answer and stop)")
	}
	if q := e.queued(t); len(q) != 2 || q[0] != "Answer to: Pick DB" {
		t.Fatalf("answer should be at the front of the queue, got %v", q)
	}
	// The card itself is marked resolved (clients stop pinning it), and keeps
	// its anchor and tool-call link.
	resolved, err := attRepo.Find(e.ctx, testScope, drainSessionID, "card1")
	if err != nil || resolved == nil {
		t.Fatalf("card lookup: %v %v", resolved, err)
	}
	if !strings.Contains(resolved.PayloadJSON, `"status":"answered"`) || !strings.Contains(resolved.PayloadJSON, `"title":"Pick DB"`) {
		t.Fatalf("card not marked answered (or payload lost): %s", resolved.PayloadJSON)
	}
	if resolved.AfterToolCallID != "call_1" || resolved.AnchorMessageID != "call_1" {
		t.Fatalf("card lost its anchor/tool call: %+v", resolved)
	}

	// Re-answer while still queued: same entry, new answer, nothing added.
	res, err = e.impl.AnswerAskCard(e.ctx, &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "card1", ResponseJson: choiceAnswer("card1", "Postgres")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetUpdatedExisting() {
		t.Fatalf("re-answer while queued should update, got %+v", res)
	}
	lq, err := e.impl.ListQueue(e.ctx, &llmv1.ListQueueRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(lq.GetQueue()) != 2 {
		t.Fatalf("re-answer must not add an entry, queue=%v", e.queued(t))
	}
	head := lq.GetQueue()[0]
	if atts := head.GetAttachments(); len(atts) != 1 || !strings.Contains(atts[0].GetContent(), "Selected: Postgres.") {
		t.Fatalf("queued answer not replaced: %+v", head.GetAttachments())
	}

	// Drain: the answer runs first, persisted on its own user message.
	e.setPaused(t, false)
	e.settle(t)
	if got := e.userTurns(); len(got) != 2 || got[0] != "Answer to: Pick DB" {
		t.Fatalf("want the answer turn first, got %v", got)
	}
	sent, err := attRepo.Find(e.ctx, testScope, drainSessionID, "card1:response")
	if err != nil || sent == nil {
		t.Fatalf("answer not persisted: %v %v", sent, err)
	}
	if sent.AnchorMessageID != "queued_"+head.GetId() {
		t.Fatalf("answer anchored to %q, want the answer's user message %q", sent.AnchorMessageID, "queued_"+head.GetId())
	}
	if want := `In response to "Pick DB": Selected: Postgres.`; sent.Content != want {
		t.Fatalf("summary = %q, want %q", sent.Content, want)
	}

	// Re-answer after it was sent: updated in place, no new turn queued.
	res, err = e.impl.AnswerAskCard(e.ctx, &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "card1", ResponseJson: choiceAnswer("card1", "MySQL")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetUpdatedExisting() || res.Entry != nil {
		t.Fatalf("re-answer after send should update in place, got %+v", res)
	}
	if q := e.queued(t); len(q) != 0 {
		t.Fatalf("re-answer after send must not queue anything, got %v", q)
	}
	sent, err = attRepo.Find(e.ctx, testScope, drainSessionID, "card1:response")
	if err != nil || sent == nil || !strings.Contains(sent.Content, "Selected: MySQL.") || sent.AnchorMessageID != "queued_"+head.GetId() {
		t.Fatalf("in-place update wrong: %+v %v", sent, err)
	}
}

func TestAnswerAskCard_continueWithToolsLeavesToolsEnabled(t *testing.T) {
	e := newDrainEnv(t)
	attRepo := store.NewChatAttachmentRepo(e.db)
	e.impl.Attachments = attRepo
	if err := attRepo.Upsert(e.ctx, testScope, drainSessionID, "call_1", store.AttachmentInput{
		ID: "card1", Kind: "ask_card", AfterToolCallID: "call_1",
		PayloadJSON: `{"id":"card1","title":"Pick DB","text":"","type":"choice","allowAdditionalText":false,"choice":{"choices":["Postgres","MySQL"],"allowMultiple":false},"status":"pending"}`,
	}); err != nil {
		t.Fatal(err)
	}
	e.setPaused(t, true)

	res, err := e.impl.AnswerAskCard(e.ctx, &llmv1.AnswerAskCardRequest{
		SessionId: drainSessionID, CardId: "card1", ResponseJson: choiceAnswer("card1", "MySQL"), ContinueWithTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetEntry() == nil {
		t.Fatalf("first answer should queue a new entry, got %+v", res)
	}
	if res.GetEntry().GetTurnOptions().GetDisableTools() {
		t.Fatal("answer and continue: queued answer must run with tools enabled")
	}
	// The persisted queue row agrees (the drainer reads it, not the response).
	lq, err := e.impl.ListQueue(e.ctx, &llmv1.ListQueueRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(lq.GetQueue()) != 1 || lq.GetQueue()[0].GetTurnOptions().GetDisableTools() {
		t.Fatalf("queued row should have tools enabled, got %+v", lq.GetQueue())
	}
}

func TestAnswerAskCard_rejectsNonAgentAndMismatchedAnswers(t *testing.T) {
	e := newDrainEnv(t)
	attRepo := store.NewChatAttachmentRepo(e.db)
	e.impl.Attachments = attRepo
	if err := attRepo.Upsert(e.ctx, testScope, drainSessionID, "m1", store.AttachmentInput{
		ID: "hostcard", Kind: "ask_card", PayloadJSON: `{"id":"hostcard","title":"x","type":"text"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := attRepo.Upsert(e.ctx, testScope, drainSessionID, "call_2", store.AttachmentInput{
		ID: "card2", Kind: "ask_card", AfterToolCallID: "call_2", PayloadJSON: `{"id":"card2","title":"y","type":"text"}`,
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		req  *llmv1.AnswerAskCardRequest
		code codes.Code
	}{
		{"unknown card", &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "nope", ResponseJson: `{"cardId":"nope","type":"text"}`}, codes.NotFound},
		{"host card", &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "hostcard", ResponseJson: `{"cardId":"hostcard","type":"text"}`}, codes.FailedPrecondition},
		{"wrong cardId", &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "card2", ResponseJson: `{"cardId":"other","type":"text"}`}, codes.InvalidArgument},
		{"wrong type", &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "card2", ResponseJson: choiceAnswer("card2", "a")}, codes.InvalidArgument},
		{"bad json", &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "card2", ResponseJson: `{`}, codes.InvalidArgument},
	}
	for _, c := range cases {
		_, err := e.impl.AnswerAskCard(e.ctx, c.req)
		if status.Code(err) != c.code {
			t.Errorf("%s: code=%v (%v), want %v", c.name, status.Code(err), err, c.code)
		}
	}
	if q := e.queued(t); len(q) != 0 {
		t.Fatalf("rejected answers must not queue anything, got %v", q)
	}
}

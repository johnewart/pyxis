package server

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc"
)

// askCardStream is a SubscribeAskCards stream with no client: Send delivers
// into a channel.
type askCardStream struct {
	grpc.ServerStream
	ctx context.Context
	out chan *llmv1.AskCardChange
}

func (s *askCardStream) Context() context.Context { return s.ctx }
func (s *askCardStream) Send(c *llmv1.AskCardChange) error {
	s.out <- c
	return nil
}

func (s *askCardStream) next(t *testing.T) *llmv1.AskCardChange {
	t.Helper()
	select {
	case c := <-s.out:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no ask card change")
		return nil
	}
}

// nextFor skips changes about other cards (e.g. the closed-state report of a
// hand-made card, which subscribers ignore as unknown).
func (s *askCardStream) nextFor(t *testing.T, cardID string) *llmv1.AskCardChange {
	t.Helper()
	for {
		if c := s.next(t); c.GetCard().GetCardId() == cardID {
			return c
		}
	}
}

func putAgentCard(t *testing.T, e *drainEnv, repo *store.ChatAttachmentRepo, id string) {
	t.Helper()
	if err := repo.Upsert(e.ctx, testScope, drainSessionID, "call_"+id, store.AttachmentInput{
		ID: id, Kind: store.AskCardKind, AfterToolCallID: "call_" + id,
		PayloadJSON: `{"id":"` + id + `","title":"Pick DB","text":"","type":"choice","allowAdditionalText":false,"choice":{"choices":["Postgres","MySQL"],"allowMultiple":false},"status":"pending"}`,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAskCards_listAndFeedFollowAskAnswerAndDismiss(t *testing.T) {
	e := newDrainEnv(t)
	repo := store.NewChatAttachmentRepo(e.db)
	e.impl.Attachments = repo
	e.setPaused(t, true) // keep the answer queued; this test is about the cards

	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	stream := &askCardStream{ctx: ctx, out: make(chan *llmv1.AskCardChange, 16)}
	done := make(chan error, 1)
	go func() { done <- e.impl.SubscribeAskCards(&llmv1.SubscribeAskCardsRequest{}, stream) }()
	// The subscription is taken synchronously at the top of the handler;
	// give the goroutine a moment to get there.
	time.Sleep(50 * time.Millisecond)

	// Each write waits for its change: the test DB is a shared-cache in-memory
	// SQLite, which refuses a write while the feed goroutine is reading.
	// Not an agent card (no tool call: a hand-made card): reported closed.
	if err := repo.Upsert(e.ctx, testScope, drainSessionID, "m", store.AttachmentInput{ID: "manual", Kind: store.AskCardKind, PayloadJSON: `{"status":"pending"}`}); err != nil {
		t.Fatal(err)
	}
	if c := stream.nextFor(t, "manual"); c.GetOpen() {
		t.Fatalf("a hand-made card is not an agent question: %+v", c)
	}
	for _, want := range []string{"c1", "c2"} {
		putAgentCard(t, e, repo, want)
		c := stream.nextFor(t, want)
		if !c.GetOpen() || c.GetCard().GetSessionId() != drainSessionID {
			t.Fatalf("want %s opened, got %+v", want, c)
		}
	}
	// Another kind: no change at all.
	if err := repo.Upsert(e.ctx, testScope, drainSessionID, "m", store.AttachmentInput{ID: "run", Kind: "build_run", PayloadJSON: `{}`}); err != nil {
		t.Fatal(err)
	}

	list, err := e.impl.ListOpenAskCards(e.ctx, &llmv1.ListOpenAskCardsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := list.GetCards(); len(got) != 2 || got[0].GetCardId() != "c1" || got[1].GetCardId() != "c2" {
		t.Fatalf("open cards = %+v", got)
	}

	// Answering closes c1 (the card is rewritten "answered").
	if _, err := e.impl.AnswerAskCard(e.ctx, &llmv1.AnswerAskCardRequest{SessionId: drainSessionID, CardId: "c1", ResponseJson: choiceAnswer("c1", "MySQL")}); err != nil {
		t.Fatal(err)
	}
	if c := stream.nextFor(t, "c1"); c.GetOpen() {
		t.Fatalf("want c1 closed, got %+v", c)
	}
	// Dismissing (deleting) closes c2.
	if err := repo.Delete(e.ctx, testScope, drainSessionID, "c2"); err != nil {
		t.Fatal(err)
	}
	if c := stream.nextFor(t, "c2"); c.GetOpen() {
		t.Fatalf("want c2 closed, got %+v", c)
	}

	list, err = e.impl.ListOpenAskCards(e.ctx, &llmv1.ListOpenAskCardsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetCards()) != 0 {
		t.Fatalf("no card should be open, got %+v", list.GetCards())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SubscribeAskCards did not end with its context")
	}
}

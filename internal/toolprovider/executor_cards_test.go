package toolprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/toolseam"
)

type recordingSink struct {
	inv   toolseam.Invocation
	cards []toolseam.Card
	err   error
}

func (s *recordingSink) ShowCards(_ context.Context, inv toolseam.Invocation, cards []toolseam.Card) error {
	s.inv, s.cards = inv, append(s.cards, cards...)
	return s.err
}

func outputWithCards(id, content string, cards ...*llmv1.MessageAttachment) *llmv1.ToolCallResult {
	r := output(id, content, false)
	r.GetOutput().Cards = cards
	return r
}

// An executor's cards reach the sink on the call's context (the server's,
// installed by RunWithHandlers), with the invocation llmd dispatched.
func TestExecutorCallDeliversCards(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	sink := &recordingSink{}
	res := goCall(toolseam.WithCardSink(context.Background(), sink), hub, d)
	tc := recvCall(t, st)
	// A card well over MaxResultBytes: only the text part is held to that cap.
	big := strings.Repeat("x", 2*MaxResultBytes)
	reply(t, st, outputWithCards(tc.GetCallId(), "shown", &llmv1.MessageAttachment{
		Id: "c1", Kind: "llmd.display", PayloadJson: `{"id":"c1","type":"chart","body":{"s":"` + big + `"}}`,
		AfterToolCallId: strp("ignored"),
	}))
	r := wait(t, res, 5*time.Second)
	if !r.ok || r.content != "shown" {
		t.Fatalf("got %+v", r)
	}
	if len(sink.cards) != 1 || sink.cards[0].ID != "c1" || sink.cards[0].Kind != "llmd.display" {
		t.Fatalf("sink got %+v", sink.cards)
	}
	if sink.inv.SessionID != "sess" || sink.inv.Name != "deploy" {
		t.Fatalf("sink invocation = %+v", sink.inv)
	}
}

// A card the sink cannot show fails the call, and the model is told why.
func TestExecutorCallCardsThatCannotBeShownFailTheCall(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	sink := &recordingSink{err: errors.New("store down")}
	res := goCall(toolseam.WithCardSink(context.Background(), sink), hub, d)
	tc := recvCall(t, st)
	reply(t, st, outputWithCards(tc.GetCallId(), "shown", &llmv1.MessageAttachment{Id: "c1", Kind: "k", PayloadJson: "{}"}))
	r := wait(t, res, 2*time.Second)
	if r.ok || !strings.Contains(r.content, "could not be shown to the user: store down") || r.errMsg != "store down" {
		t.Fatalf("got %+v", r)
	}
}

// Cards on an error result are dropped: there is nothing to show.
func TestExecutorCallErrorResultCardsIgnored(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	sink := &recordingSink{}
	res := goCall(toolseam.WithCardSink(context.Background(), sink), hub, d)
	tc := recvCall(t, st)
	r := outputWithCards(tc.GetCallId(), "boom", &llmv1.MessageAttachment{Id: "c1", Kind: "k", PayloadJson: "{}"})
	r.GetOutput().IsError = true
	reply(t, st, r)
	if got := wait(t, res, 2*time.Second); got.ok {
		t.Fatalf("got %+v", got)
	}
	if len(sink.cards) != 0 {
		t.Fatalf("cards of a failed call were shown: %+v", sink.cards)
	}
}

// Cards are capped on their own (MaxCardsBytes), not by MaxResultBytes.
func TestExecutorCallOversizeCards(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	res := goCall(toolseam.WithCardSink(context.Background(), &recordingSink{}), hub, d)
	tc := recvCall(t, st)
	r := outputWithCards(tc.GetCallId(), "x", &llmv1.MessageAttachment{Id: "c1", Kind: "k", PayloadJson: strings.Repeat("y", MaxCardsBytes+1)})
	if !hub.DeliverCallResult("t1", r) {
		t.Fatal("result not routed")
	}
	got := wait(t, res, 2*time.Second)
	if got.ok || !strings.Contains(got.errMsg, "cards too large") {
		t.Fatalf("got %+v", got)
	}
}

func strp(s string) *string { return &s }

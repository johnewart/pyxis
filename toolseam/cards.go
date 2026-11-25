package toolseam

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Card is a display-only attachment a tool returns with its result: an image,
// a chart, or any host-defined card (see llmd/visualcards for the built-in
// display cards and their payload). llmd puts it into the calling session's
// transcript right after this tool call; the model never sees PayloadJSON.
//
// A tool that returns cards needs no session client of its own: whatever
// dispatched the call (the chat loop, MCP, InvokeTool, an executor stream)
// delivers them through the server's CardSink.
type Card struct {
	// ID is unique within the session; returning an existing ID replaces that card.
	ID   string
	Kind string
	// PayloadJSON is for the UI only; llmd never sends it to the model.
	PayloadJSON string
	// Content, when set, is spliced into what the provider sees (e.g. a one-line
	// "[Shown to the user: …]"). Usually empty: the tool result already says
	// what was shown.
	Content string
}

// CardSink puts a tool call's cards into its session. The server installs one
// on its handler set; dispatch code calls it through ShowCards.
type CardSink interface {
	ShowCards(ctx context.Context, inv Invocation, cards []Card) error
}

// ErrNoCardSink means cards were returned on a dispatch path with no sink:
// nothing would show them, so the call fails rather than let the tool's
// result tell the model the user saw something they did not.
var ErrNoCardSink = errors.New("this host does not display tool cards")

type cardSinkKey struct{}

// WithCardSink attaches sink to ctx for one dispatch. nil sink ⇒ ctx unchanged.
func WithCardSink(ctx context.Context, sink CardSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, cardSinkKey{}, sink)
}

// CardSinkFrom returns the sink on ctx, or nil.
func CardSinkFrom(ctx context.Context) CardSink {
	s, _ := ctx.Value(cardSinkKey{}).(CardSink)
	return s
}

// ShowCards validates and delivers a successful call's cards through the sink
// on ctx. No cards ⇒ nil. The returned error is the reason the call must be
// reported as failed.
func ShowCards(ctx context.Context, inv Invocation, cards []Card) error {
	if len(cards) == 0 {
		return nil
	}
	for i, c := range cards {
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Kind) == "" {
			return fmt.Errorf("card %d needs an id and a kind", i+1)
		}
	}
	if strings.TrimSpace(inv.SessionID) == "" {
		return errors.New("tool call has no session, so its cards have nowhere to appear")
	}
	sink := CardSinkFrom(ctx)
	if sink == nil {
		return ErrNoCardSink
	}
	return sink.ShowCards(ctx, inv, cards)
}

// cardFailure is the tool result for a call whose work succeeded but whose
// cards could not be shown.
func cardFailure(content string, err error) string {
	msg := fmt.Sprintf("the tool ran, but its result could not be shown to the user: %v", err)
	if strings.TrimSpace(content) == "" {
		return msg
	}
	return msg + "\n\n" + content
}

// CardFailure is cardFailure for dispatch paths outside this package
// (executor streams).
func CardFailure(content string, err error) string { return cardFailure(content, err) }

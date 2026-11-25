package server

import (
	"context"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Open ask cards across sessions.
//
// An agent's question (ask_user) is an ask_card attachment on its session;
// it stays open until answered (AnswerAskCard), dismissed or deleted. These
// two RPCs let a host show every open question in one place - including
// those of sessions nobody has open - without subscribing to each session.
//
// The feed follows the attachment repo's own change fan-out (every write
// goes through it: the ask_user tool, AnswerAskCard, Upsert/Delete RPCs), and
// resolves each change to the card's current state, so a late or reordered
// change still reports the truth.

func openAskCardProto(r store.OpenAskCardRecord) *llmv1.OpenAskCard {
	return &llmv1.OpenAskCard{
		ScopeId: r.ScopeID, SessionId: r.SessionID, CardId: r.CardID,
		PayloadJson: r.PayloadJSON, CreatedAtMs: r.CreatedAt.UnixMilli(),
	}
}

// ListOpenAskCards returns every open agent ask card in the caller's tenant,
// or in its scope when one is given.
func (s *LlmServer) ListOpenAskCards(ctx context.Context, _ *llmv1.ListOpenAskCardsRequest) (*llmv1.ListOpenAskCardsResponse, error) {
	if s.Attachments == nil {
		return nil, status.Error(codes.FailedPrecondition, "attachment store not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := s.Attachments.ListOpenAskCards(ctx, sc)
	if err != nil {
		return nil, attachmentStoreErr(err)
	}
	out := &llmv1.ListOpenAskCardsResponse{Cards: make([]*llmv1.OpenAskCard, 0, len(recs))}
	for _, r := range recs {
		out.Cards = append(out.Cards, openAskCardProto(r))
	}
	return out, nil
}

// askCardIDOf maps an attachment change to the ask card it is about: the
// card itself, or the card an answer belongs to. ok=false for anything else.
// Deletes carry no kind, so any deleted id is treated as a possible card.
func askCardIDOf(c store.AttachmentChange) (string, bool) {
	switch {
	case c.Kind == store.AskCardKind:
		return c.AttachmentID, true
	case c.Kind == store.AskCardResponseKind:
		return strings.TrimSuffix(c.AttachmentID, ":response"), true
	case c.Deleted:
		return strings.TrimSuffix(c.AttachmentID, ":response"), true
	}
	return "", false
}

func scopeCovers(sub, sc serverutil.Scope) bool {
	return sub.TenantID == sc.TenantID && (sub.IsTenantWide() || sub.ScopeID == sc.ScopeID)
}

// SubscribeAskCards streams ask cards opening and closing in the caller's
// tenant (or scope) until the client goes away. It sends no initial state:
// subscribe, then ListOpenAskCards, then apply changes.
func (s *LlmServer) SubscribeAskCards(_ *llmv1.SubscribeAskCardsRequest, stream grpc.ServerStreamingServer[llmv1.AskCardChange]) error {
	if s.Attachments == nil {
		return status.Error(codes.FailedPrecondition, "attachment store not configured")
	}
	ctx := stream.Context()
	sub, err := s.scope(ctx)
	if err != nil {
		return err
	}
	changes := s.Attachments.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case c, ok := <-changes:
			if !ok {
				return nil
			}
			if !scopeCovers(sub, c.Scope) {
				continue
			}
			cardID, ok := askCardIDOf(c)
			if !ok || cardID == "" {
				continue
			}
			rec, err := s.Attachments.OpenAskCard(ctx, c.Scope, c.SessionID, cardID)
			if err != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn, "SubscribeAskCards: %s session=%q card=%q: %v", c.Scope.String(), c.SessionID, cardID, err)
				continue
			}
			ev := &llmv1.AskCardChange{Open: rec != nil}
			if rec != nil {
				ev.Card = openAskCardProto(*rec)
			} else {
				ev.Card = &llmv1.OpenAskCard{ScopeId: c.Scope.ScopeID, SessionId: c.SessionID, CardId: cardID}
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

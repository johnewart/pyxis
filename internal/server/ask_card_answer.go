package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Ask-card answers.
//
// An agent asks via the ask_user tool (internal/toolexec askUserHandler), which
// persists an ask_card attachment and ends the turn. AnswerAskCard is the other
// half: the answer becomes a USER message ("Answer to: <title>") carrying an
// ask_card_response attachment, queued at the front so the agent resumes on it
// (with tools disabled unless the request sets continue_with_tools).
// Anchoring the answer to that user message (not the agent's message the card
// sits on) is what puts it in the prompt: send-time injection only splices
// attachment content into user messages (injectTrackedContextForSend).

const (
	askCardKind         = "ask_card"
	askCardResponseKind = "ask_card_response"
)

// askCardResponseWire mirrors packages/core/src/askCard.ts AskCardResponse.
type askCardResponseWire struct {
	CardID         string `json:"cardId"`
	Type           string `json:"type"`
	AdditionalText string `json:"additionalText,omitempty"`
	Choice         *struct {
		Selected []string `json:"selected"`
	} `json:"choice,omitempty"`
	File *struct {
		FileName  string `json:"fileName"`
		MimeType  string `json:"mimeType"`
		SizeBytes int64  `json:"sizeBytes"`
	} `json:"file,omitempty"`
}

// askCardResponseAttachmentID is stable per card (matches the TS
// buildAskCardResponseAttachment id), so a re-answer replaces the same row.
func askCardResponseAttachmentID(cardID string) string { return cardID + ":response" }

// synthesizeAskCardResponseContent is the provider-facing summary of an
// answer: a sentence, never the raw structured answer and never file bytes.
// Go port of packages/core/src/askCard.ts synthesizeAskCardResponseContent —
// keep the two in sync (the TS one remains for display/offline use).
func synthesizeAskCardResponseContent(title string, r askCardResponseWire) string {
	var parts []string
	additional := strings.TrimSpace(r.AdditionalText)
	switch {
	case r.Type == "choice" && r.Choice != nil:
		if len(r.Choice.Selected) > 0 {
			parts = append(parts, "Selected: "+strings.Join(r.Choice.Selected, ", ")+".")
		} else {
			parts = append(parts, "No option selected.")
		}
	case r.Type == "file" && r.File != nil:
		kb := int64(math.Max(1, math.Round(float64(r.File.SizeBytes)/1024)))
		mime := r.File.MimeType
		if mime == "" {
			mime = "unknown type"
		}
		parts = append(parts, fmt.Sprintf("Uploaded file: %s (%d KB, %s).", r.File.FileName, kb, mime))
	default:
		if additional != "" {
			parts = append(parts, additional)
		} else {
			parts = append(parts, "No answer provided.")
		}
	}
	if r.Type != "text" && additional != "" {
		parts = append(parts, "Additional note: "+additional)
	}
	t := strings.TrimSpace(title)
	if t == "" {
		t = "a question"
	}
	// Plain quotes (not %q) to match the TS template literal byte-for-byte.
	return `In response to "` + t + `": ` + strings.Join(parts, " ")
}

func (s *LlmServer) AnswerAskCard(ctx context.Context, req *llmv1.AnswerAskCardRequest) (*llmv1.AnswerAskCardResponse, error) {
	if s.Attachments == nil {
		return nil, status.Error(codes.FailedPrecondition, "attachment store not configured")
	}
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	cardID := strings.TrimSpace(req.GetCardId())
	if cardID == "" {
		return nil, status.Error(codes.InvalidArgument, "card_id is required")
	}

	// The card must be an agent card: an ask_card the ask_user tool created
	// (it always records the tool call it came from).
	card, err := s.Attachments.Find(ctx, sc, sid, cardID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load ask card: %v", err)
	}
	if card == nil {
		return nil, status.Errorf(codes.NotFound, "no ask card %q on this session", cardID)
	}
	if card.Kind != askCardKind || strings.TrimSpace(card.AfterToolCallID) == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "attachment %q is not an agent ask card", cardID)
	}
	var cardPayload struct {
		Title string `json:"title"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal([]byte(card.PayloadJSON), &cardPayload); err != nil {
		return nil, status.Errorf(codes.Internal, "ask card %q: undecodable payload: %v", cardID, err)
	}

	var resp askCardResponseWire
	if err := json.Unmarshal([]byte(req.GetResponseJson()), &resp); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "response_json: %v", err)
	}
	if resp.CardID != cardID {
		return nil, status.Errorf(codes.InvalidArgument, "response cardId %q does not match card_id %q", resp.CardID, cardID)
	}
	if resp.Type != cardPayload.Type {
		return nil, status.Errorf(codes.InvalidArgument, "response type %q does not match card type %q", resp.Type, cardPayload.Type)
	}

	answer := store.QueuedAttachment{
		ID:          askCardResponseAttachmentID(cardID),
		Kind:        askCardResponseKind,
		PayloadJSON: req.GetResponseJson(),
		Content:     synthesizeAskCardResponseContent(cardPayload.Title, resp),
	}

	// 1. Already sent: update the persisted answer in place; no new turn.
	if existing, err := s.Attachments.Find(ctx, sc, sid, answer.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "load existing answer: %v", err)
	} else if existing != nil {
		if err := s.upsertAnswerRow(ctx, sc, sid, existing.AnchorMessageID, answer); err != nil {
			return nil, err
		}
		return &llmv1.AnswerAskCardResponse{UpdatedExisting: true}, nil
	}

	// 2. Still queued: replace the queued entry's answer.
	rows, err := s.SessionQueue.List(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list queue: %v", err)
	}
	for _, row := range rows {
		atts, aerr := row.Attachments()
		if aerr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "AnswerAskCard: queue entry=%q undecodable attachments: %v", row.ID, aerr)
			continue
		}
		idx := -1
		for i, a := range atts {
			if a.ID == answer.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		atts[idx] = answer
		ok, err := s.SessionQueue.ReplacePendingAttachments(ctx, sc, sid, row.ID, atts)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "update queued answer: %v", err)
		}
		if !ok {
			// Claimed between List and here: its turn is starting and will
			// persist the old answer anchored to the entry's message. Write the
			// new one over it there instead.
			if err := s.upsertAnswerRow(ctx, sc, sid, "queued_"+row.ID, answer); err != nil {
				return nil, err
			}
			return &llmv1.AnswerAskCardResponse{UpdatedExisting: true}, nil
		}
		row.AttachmentsJSON = mustJSON(atts)
		s.announceQueueChanged(ctx, sc, sid)
		return &llmv1.AnswerAskCardResponse{Entry: queuedMessageToProto(row), UpdatedExisting: true}, nil
	}

	// 3. First answer: queue it at the front.
	title := strings.TrimSpace(cardPayload.Title)
	if title == "" {
		title = "a question"
	}
	// "Answer and stop" (the default) runs the answer's turn with no tools, so
	// the agent acknowledges and stops rather than treating the answer as a
	// go-ahead. "Answer and continue" (continue_with_tools) lets it carry on.
	row, err := s.SessionQueue.EnqueueFront(ctx, sc, sid, "Answer to: "+title, []store.QueuedAttachment{answer},
		store.QueuedTurnOptions{DisableTools: !req.GetContinueWithTools()})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "enqueue answer: %v", err)
	}
	s.markAskCardAnswered(ctx, sc, sid, card)
	s.announceQueueChanged(ctx, sc, sid)
	s.kickQueueDrain(sc, sid)
	return &llmv1.AnswerAskCardResponse{Entry: queuedMessageToProto(*row)}, nil
}

// markAskCardAnswered rewrites the card's own payload to status "answered" so
// the stored card says it is resolved (clients stop giving it a row; see
// @pyxis-llm/core appendAnchorlessCardMessages, which only pins "pending" cards).
// Best-effort: the answer is already queued; a failure here is logged, and
// clients also treat a card with a `<id>:response` as resolved.
func (s *LlmServer) markAskCardAnswered(ctx context.Context, sc serverutil.Scope, sid string, card *store.AttachmentRecord) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(card.PayloadJSON), &payload); err != nil || payload == nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "AnswerAskCard: card=%q payload not an object, status not updated: %v", card.ID, err)
		return
	}
	if payload["status"] == "answered" {
		return
	}
	payload["status"] = "answered"
	raw, err := json.Marshal(payload)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "AnswerAskCard: card=%q encode payload: %v", card.ID, err)
		return
	}
	if err := s.Attachments.Upsert(ctx, sc, sid, card.AnchorMessageID, store.AttachmentInput{
		ID: card.ID, Kind: card.Kind, PayloadJSON: string(raw), AfterToolCallID: card.AfterToolCallID, Content: card.Content,
	}); err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "AnswerAskCard: %s session=%q card=%q mark answered failed: %v", sc.String(), sid, card.ID, err)
		return
	}
	frame := &llmv1.EvAttachmentUpserted{
		SessionId: sid, AttachmentId: card.ID, Kind: card.Kind, MessageId: card.AnchorMessageID, PayloadJson: string(raw),
	}
	if card.AfterToolCallID != "" {
		frame.AfterToolCallId = card.AfterToolCallID
	}
	s.announceAttachmentFrame(ctx, sc, sid, &llmv1.ConversationStreamFrame{
		Frame: &llmv1.ConversationStreamFrame_AttachmentUpserted{AttachmentUpserted: frame},
	})
}

// upsertAnswerRow writes the answer attachment anchored to anchor and tells
// every window.
func (s *LlmServer) upsertAnswerRow(ctx context.Context, sc serverutil.Scope, sid, anchor string, a store.QueuedAttachment) error {
	if err := s.Attachments.Upsert(ctx, sc, sid, anchor, store.AttachmentInput{
		ID: a.ID, Kind: a.Kind, PayloadJSON: a.PayloadJSON, Content: a.Content,
	}); err != nil {
		return attachmentStoreErr(err)
	}
	s.announceAttachmentFrame(ctx, sc, sid, &llmv1.ConversationStreamFrame{
		Frame: &llmv1.ConversationStreamFrame_AttachmentUpserted{
			AttachmentUpserted: &llmv1.EvAttachmentUpserted{
				SessionId: sid, AttachmentId: a.ID, Kind: a.Kind, MessageId: anchor, PayloadJson: a.PayloadJSON,
			},
		},
	})
	return nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "AnswerAskCard: encode attachments: %v", err)
		return ""
	}
	return string(b)
}

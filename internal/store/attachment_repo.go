package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/llmdlog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Sentinel errors so handlers can map to the right gRPC status codes.
var (
	ErrAttachmentInvalidArgs     = errors.New("attachment: sc.TenantID, sc.ScopeID, sessionID and ids are required")
	ErrAttachmentAnchorNotFound  = errors.New("attachment: anchor message not found in session")
	ErrAttachmentMessageNotFound = errors.New("attachment: assistant message not found in session")
)

// ChatAttachmentRepo persists session UI-artifact attachments (ask cards, embedded
// build runs) as kind="attachment" rows in llmd_chat_messages. This is the ONLY
// write/read path for those rows — a client reaches them exclusively over gRPC and
// never opens llmd's SQLite directly. Scoped per call by (scope, sessionID).
//
// Every committed write is also published to in-process subscribers
// (Subscribe), so a reader can follow attachments across sessions without
// polling each one.
type ChatAttachmentRepo struct {
	db *gorm.DB

	mu   sync.Mutex
	subs map[chan AttachmentChange]struct{}
}

// NewChatAttachmentRepo returns a repo, or nil when db is nil.
func NewChatAttachmentRepo(db *gorm.DB) *ChatAttachmentRepo {
	if db == nil {
		return nil
	}
	return &ChatAttachmentRepo{db: db, subs: map[chan AttachmentChange]struct{}{}}
}

// AttachmentChange names one attachment that was written or deleted. It
// carries ids only: a subscriber reads the current state (Find) itself, so a
// late or reordered change still ends at the truth.
type AttachmentChange struct {
	Scope        serverutil.Scope
	SessionID    string
	AttachmentID string
	// Kind is set on writes; empty on deletes (the row is gone).
	Kind    string
	Deleted bool
}

// attachmentSubscriberBuffer bounds how far a subscriber may fall behind
// before changes to it are dropped (see publish).
const attachmentSubscriberBuffer = 256

// Subscribe returns a channel of every attachment change committed after the
// call, closed when ctx ends. Delivery never blocks a writer: a subscriber a
// full buffer behind misses changes and must re-read current state.
func (r *ChatAttachmentRepo) Subscribe(ctx context.Context) <-chan AttachmentChange {
	ch := make(chan AttachmentChange, attachmentSubscriberBuffer)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
		close(ch)
	}()
	return ch
}

func (r *ChatAttachmentRepo) publish(c AttachmentChange) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.subs {
		select {
		case ch <- c:
		default:
			llmdlog.Stdout("store", llmdlog.LevelWarn, "attachment subscriber behind; dropped change session=%q attachment=%q", c.SessionID, c.AttachmentID)
		}
	}
}

// AttachmentInput is one UI attachment to persist (mirrors llmv1.MessageAttachment).
type AttachmentInput struct {
	ID              string
	Kind            string
	PayloadJSON     string
	AfterToolCallID string
	// Content is provider-facing text (mirrors MessageAttachment.content). When
	// non-empty, injectTrackedContextForSend splices it into the outbound message
	// at send-time; the persisted row is never mutated. Empty = display-only,
	// matching the historical (pre-content) behavior.
	Content string
}

// AttachmentRowRaw is one persisted attachment row as the client re-hydrates it:
// Content is the serialized payload (includes the stored anchor message id).
type AttachmentRowRaw struct {
	Content    string
	ToolCallID string
}

// attachmentRowContent is the JSON shape stored in the content column. It matches the
// client's llmdMessageAttachmentRowContent/...FromRowContent (snake_case keys).
type attachmentRowContent struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	PayloadJSON     string `json:"payload_json"`
	AnchorMessageID string `json:"anchor_message_id"`
	AfterToolCallID string `json:"after_tool_call_id,omitempty"`
	// TurnMessageID is the user message that opened the turn the attachment
	// was first written in (see attachmentTurnMessageID). Clients place a card
	// whose tool call is not in the transcript at the end of this turn.
	TurnMessageID string `json:"turn_message_id,omitempty"`
	// Content is provider-facing text (mirrors MessageAttachment.content).
	Content string `json:"content,omitempty"`
}

// attachmentTurnMessageID returns the id of the user message that opened the
// turn an attachment belongs to: the one recorded by an earlier write of the
// same attachment, else the session's latest user message (the turn running
// now). A client needs it to place a card made by a tool whose call is not in
// the transcript: a CLI provider (Claude Code, Cursor) calls tools over MCP,
// whose call ids llmd mints and never stores as transcript rows, so without
// it such a card can only go at the very end and stays below every later
// message. "" when the session has no user message yet.
func attachmentTurnMessageID(tx *gorm.DB, sc serverutil.Scope, sessionID, attachmentID string) (string, error) {
	var existing []ChatMessageRow
	if err := tx.Model(&ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND kind = ? AND message_id = ?",
			sc.TenantID, sc.ScopeID, sessionID, ChatMsgKindAttachment, attachmentID).
		Order("id DESC").Limit(1).Find(&existing).Error; err != nil {
		return "", fmt.Errorf("read existing attachment %q: %w", attachmentID, err)
	}
	if len(existing) > 0 {
		var prev attachmentRowContent
		if err := json.Unmarshal([]byte(existing[0].Content), &prev); err != nil {
			llmdlog.Stdout("store", llmdlog.LevelWarn,
				"attachment row undecodable; recording the current turn for it: session=%q attachment=%q: %v", sessionID, attachmentID, err)
		} else if id := strings.TrimSpace(prev.TurnMessageID); id != "" {
			return id, nil
		}
	}
	var ids []string
	if err := tx.Model(&ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND kind = ?",
			sc.TenantID, sc.ScopeID, sessionID, ChatMsgKindUser).
		Order("id DESC").Limit(1).Pluck("message_id", &ids).Error; err != nil {
		return "", fmt.Errorf("find latest user message: %w", err)
	}
	if len(ids) == 0 {
		return "", nil
	}
	return strings.TrimSpace(ids[0]), nil
}

type anchorTurn struct {
	TurnID string `gorm:"column:turn_id"`
	Role   string `gorm:"column:role"`
}

// resolveAnchorTurn finds the turn the attachment should hang on: the anchor message's
// own turn, or — if that assistant row isn't flushed yet — the latest assistant turn,
// so the attachment still persists instead of being dropped. Runs against tx so it
// participates in Upsert's transaction (see ensureAnchorTurn, which creates a fresh
// placeholder turn on the same tx when this finds nothing at all).
func resolveAnchorTurn(tx *gorm.DB, sc serverutil.Scope, sessionID, anchorMessageID string) (anchorTurn, bool) {
	var row anchorTurn
	err := tx.Model(&ChatMessageRow{}).
		Select("turn_id, role").
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND message_id = ?", sc.TenantID, sc.ScopeID, sessionID, anchorMessageID).
		Order("id ASC").Limit(1).Scan(&row).Error
	if err == nil && strings.TrimSpace(row.TurnID) != "" {
		if strings.TrimSpace(row.Role) == "" {
			row.Role = "assistant"
		}
		return row, true
	}

	var fallback anchorTurn
	err = tx.Model(&ChatMessageRow{}).
		Select("turn_id, role").
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND role = ? AND turn_id IS NOT NULL AND TRIM(turn_id) <> ''",
			sc.TenantID, sc.ScopeID, sessionID, "assistant").
		Order("id DESC").Limit(1).Scan(&fallback).Error
	if err != nil || strings.TrimSpace(fallback.TurnID) == "" {
		return anchorTurn{}, false
	}
	if strings.TrimSpace(fallback.Role) == "" {
		fallback.Role = "assistant"
	}
	return fallback, true
}

// ensureAnchorTurn is resolveAnchorTurn plus a fallback of last resort: when a
// session has NO assistant turn at all yet (e.g. a tool that creates a
// user-facing card, such as ask_user, invoked on a session's very first
// round — the round producing it hasn't been flushed to storage yet), it
// creates a real, empty assistant message row and anchors to that instead of
// failing. This is honest rather than a hack: the row IS what it claims to be
// (an assistant turn with no text this round), not a synthetic id that looks
// like something it isn't. Runs on tx so the placeholder and the attachment
// that anchors to it commit atomically together.
func (r *ChatAttachmentRepo) ensureAnchorTurn(tx *gorm.DB, sc serverutil.Scope, sessionID, anchorMessageID string) (anchorTurn, error) {
	if anchor, ok := resolveAnchorTurn(tx, sc, sessionID, anchorMessageID); ok {
		return anchor, nil
	}
	placeholder := ChatMessageRow{
		TenantID: sc.TenantID, ScopeID: sc.ScopeID,
		SessionID: sessionID,
		TurnID:    uuid.New().String(),
		Kind:      ChatMsgKindAssistant,
		Role:      "assistant",
		MessageID: uuid.New().String(),
		Content:   "",
		CreatedAt: time.Now(),
	}
	if err := tx.Create(&placeholder).Error; err != nil {
		return anchorTurn{}, err
	}
	if err := refreshSessionLastMessageAt(tx, sc, sessionID); err != nil {
		return anchorTurn{}, err
	}
	return anchorTurn{TurnID: placeholder.TurnID, Role: placeholder.Role}, nil
}

func (r *ChatAttachmentRepo) deleteRows(ctx context.Context, sc serverutil.Scope, sessionID, attachmentID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND kind = ? AND message_id = ?",
			sc.TenantID, sc.ScopeID, sessionID, ChatMsgKindAttachment, attachmentID).
			Delete(&ChatMessageRow{}).Error; err != nil {
			return err
		}
		return refreshSessionLastMessageAt(tx, sc, sessionID)
	})
}

// Upsert persists or replaces one attachment row anchored to a message's turn.
func (r *ChatAttachmentRepo) Upsert(ctx context.Context, sc serverutil.Scope, sessionID, anchorMessageID string, att AttachmentInput) error {
	sessionID = strings.TrimSpace(sessionID)
	anchorMessageID = strings.TrimSpace(anchorMessageID)
	attachmentID := strings.TrimSpace(att.ID)
	if sc.TenantID == "" || sessionID == "" || anchorMessageID == "" || attachmentID == "" {
		return ErrAttachmentInvalidArgs
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		anchor, err := r.ensureAnchorTurn(tx, sc, sessionID, anchorMessageID)
		if err != nil {
			return err
		}
		// Read before the delete below: it keeps the turn an earlier write recorded.
		turnMessageID, err := attachmentTurnMessageID(tx, sc, sessionID, attachmentID)
		if err != nil {
			return err
		}
		content, err := json.Marshal(attachmentRowContent{
			ID:              attachmentID,
			Kind:            att.Kind,
			PayloadJSON:     att.PayloadJSON,
			AnchorMessageID: anchorMessageID,
			AfterToolCallID: strings.TrimSpace(att.AfterToolCallID),
			TurnMessageID:   turnMessageID,
			Content:         att.Content,
		})
		if err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND kind = ? AND message_id = ?",
			sc.TenantID, sc.ScopeID, sessionID, ChatMsgKindAttachment, attachmentID).
			Delete(&ChatMessageRow{}).Error; err != nil {
			return err
		}
		role := anchor.Role
		if strings.TrimSpace(role) == "" {
			role = "assistant"
		}
		if err := tx.Create(&ChatMessageRow{
			TenantID: sc.TenantID, ScopeID: sc.ScopeID,
			SessionID:  sessionID,
			TurnID:     anchor.TurnID,
			Kind:       ChatMsgKindAttachment,
			Role:       role,
			MessageID:  attachmentID,
			Content:    string(content),
			ToolCallID: strings.TrimSpace(att.AfterToolCallID),
			CreatedAt:  time.Now(),
		}).Error; err != nil {
			return err
		}
		return refreshSessionLastMessageAt(tx, sc, sessionID)
	})
	if err != nil {
		return err
	}
	r.publish(AttachmentChange{Scope: sc, SessionID: sessionID, AttachmentID: attachmentID, Kind: att.Kind})
	return nil
}

// Delete removes one attachment row.
func (r *ChatAttachmentRepo) Delete(ctx context.Context, sc serverutil.Scope, sessionID, attachmentID string) error {
	sessionID = strings.TrimSpace(sessionID)
	attachmentID = strings.TrimSpace(attachmentID)
	if sc.TenantID == "" || sessionID == "" || attachmentID == "" {
		return ErrAttachmentInvalidArgs
	}
	if err := r.deleteRows(ctx, sc, sessionID, attachmentID); err != nil {
		return err
	}
	r.publish(AttachmentChange{Scope: sc, SessionID: sessionID, AttachmentID: attachmentID, Deleted: true})
	return nil
}

// AskCardKind / AskCardResponseKind are the attachment kinds of an agent's
// question (ask_user) and of the user's answer to it.
const (
	AskCardKind         = "ask_card"
	AskCardResponseKind = "ask_card_response"
)

// AskCardResponseID is the id of the answer attachment to cardID (the TS
// buildAskCardResponseAttachment id): one per card, replaced on re-answer.
func AskCardResponseID(cardID string) string { return cardID + ":response" }

// OpenAskCardRecord is an agent ask card still waiting on the user.
type OpenAskCardRecord struct {
	ScopeID     string
	SessionID   string
	CardID      string
	PayloadJSON string
	CreatedAt   time.Time
}

// askCardOpen reports whether a stored ask card still waits: it came from
// the ask_user tool (records its tool call), its payload says "pending", and
// no answer exists. A card whose payload does not decode is not open.
func askCardOpen(c attachmentRowContent, answered bool) bool {
	if c.Kind != AskCardKind || strings.TrimSpace(c.AfterToolCallID) == "" || answered {
		return false
	}
	var p struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(c.PayloadJSON), &p) != nil {
		return false
	}
	return p.Status == "pending"
}

// ListOpenAskCards returns every open agent ask card in sc, oldest first.
// A tenant-wide sc (empty ScopeID) covers every scope of the tenant.
func (r *ChatAttachmentRepo) ListOpenAskCards(ctx context.Context, sc serverutil.Scope) ([]OpenAskCardRecord, error) {
	if sc.TenantID == "" {
		return nil, ErrAttachmentInvalidArgs
	}
	q := r.db.WithContext(ctx).Model(&ChatMessageRow{}).Where("tenant_id = ? AND kind = ?", sc.TenantID, ChatMsgKindAttachment)
	if !sc.IsTenantWide() {
		q = q.Where("scope_id = ?", sc.ScopeID)
	}
	var rows []ChatMessageRow
	if err := q.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	type key struct{ scope, session, id string }
	answered := map[key]bool{}
	parsed := make([]attachmentRowContent, len(rows))
	for i := range rows {
		if json.Unmarshal([]byte(rows[i].Content), &parsed[i]) != nil {
			continue
		}
		if parsed[i].Kind == AskCardResponseKind {
			answered[key{rows[i].ScopeID, rows[i].SessionID, parsed[i].ID}] = true
		}
	}
	out := []OpenAskCardRecord{}
	for i := range rows {
		c := parsed[i]
		if !askCardOpen(c, answered[key{rows[i].ScopeID, rows[i].SessionID, AskCardResponseID(c.ID)}]) {
			continue
		}
		out = append(out, OpenAskCardRecord{
			ScopeID: rows[i].ScopeID, SessionID: rows[i].SessionID, CardID: c.ID,
			PayloadJSON: c.PayloadJSON, CreatedAt: rows[i].CreatedAt,
		})
	}
	return out, nil
}

// OpenAskCard returns cardID in the session if it is an open agent ask card,
// else nil. Used to resolve a change into the card's current state.
func (r *ChatAttachmentRepo) OpenAskCard(ctx context.Context, sc serverutil.Scope, sessionID, cardID string) (*OpenAskCardRecord, error) {
	sessionID, cardID = strings.TrimSpace(sessionID), strings.TrimSpace(cardID)
	if sc.TenantID == "" || sessionID == "" || cardID == "" {
		return nil, ErrAttachmentInvalidArgs
	}
	var rows []ChatMessageRow
	if err := r.db.WithContext(ctx).Model(&ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND kind = ? AND message_id IN ?",
			sc.TenantID, sc.ScopeID, sessionID, ChatMsgKindAttachment, []string{cardID, AskCardResponseID(cardID)}).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var card *ChatMessageRow
	var content attachmentRowContent
	answered := false
	for i := range rows {
		var c attachmentRowContent
		if json.Unmarshal([]byte(rows[i].Content), &c) != nil {
			continue
		}
		switch c.ID {
		case cardID:
			card, content = &rows[i], c
		case AskCardResponseID(cardID):
			answered = true
		}
	}
	if card == nil || !askCardOpen(content, answered) {
		return nil, nil
	}
	return &OpenAskCardRecord{
		ScopeID: card.ScopeID, SessionID: card.SessionID, CardID: content.ID,
		PayloadJSON: content.PayloadJSON, CreatedAt: card.CreatedAt,
	}, nil
}

// LoadRows returns all attachment payload rows for a session (ask cards + embedded build runs),
// ordered oldest-first, as raw content the client parses.
func (r *ChatAttachmentRepo) LoadRows(ctx context.Context, sc serverutil.Scope, sessionID string) ([]AttachmentRowRaw, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sessionID == "" {
		return nil, nil
	}
	var rows []ChatMessageRow
	if err := r.db.WithContext(ctx).Model(&ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND kind = ?", sc.TenantID, sc.ScopeID, sessionID, ChatMsgKindAttachment).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]AttachmentRowRaw, 0, len(rows))
	for i := range rows {
		out = append(out, AttachmentRowRaw{Content: rows[i].Content, ToolCallID: rows[i].ToolCallID})
	}
	return out, nil
}

// AttachmentRecord is one persisted attachment, decoded.
type AttachmentRecord struct {
	ID              string
	Kind            string
	PayloadJSON     string
	AnchorMessageID string
	AfterToolCallID string
	Content         string
}

// Find returns the attachment with id in the session, or (nil, nil) if absent.
func (r *ChatAttachmentRepo) Find(ctx context.Context, sc serverutil.Scope, sessionID, attachmentID string) (*AttachmentRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	attachmentID = strings.TrimSpace(attachmentID)
	if sc.TenantID == "" || sessionID == "" || attachmentID == "" {
		return nil, ErrAttachmentInvalidArgs
	}
	var rows []ChatMessageRow
	if err := r.db.WithContext(ctx).Model(&ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND kind = ? AND message_id = ?",
			sc.TenantID, sc.ScopeID, sessionID, ChatMsgKindAttachment, attachmentID).
		Order("id DESC").Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	var parsed attachmentRowContent
	if err := json.Unmarshal([]byte(rows[0].Content), &parsed); err != nil {
		return nil, fmt.Errorf("attachment %q: decode row: %w", attachmentID, err)
	}
	return &AttachmentRecord{
		ID:              parsed.ID,
		Kind:            parsed.Kind,
		PayloadJSON:     parsed.PayloadJSON,
		AnchorMessageID: parsed.AnchorMessageID,
		AfterToolCallID: parsed.AfterToolCallID,
		Content:         parsed.Content,
	}, nil
}

// AttachmentInjection is one attachment's provider-facing content, keyed to the
// message it's anchored to, for splicing into the outbound clone.
type AttachmentInjection struct {
	ID              string
	AnchorMessageID string
	Content         string
}

// ListInjectableContent returns provider-facing content for every attachment in
// the session that has one, keyed by anchor message id. Rows with empty
// Content (the common, display-only case) are omitted. Decodes the stored
// JSON server-side so callers never need to know the row's wire shape.
func (r *ChatAttachmentRepo) ListInjectableContent(ctx context.Context, sc serverutil.Scope, sessionID string) ([]AttachmentInjection, error) {
	rows, err := r.LoadRows(ctx, sc, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]AttachmentInjection, 0, len(rows))
	for _, row := range rows {
		var parsed attachmentRowContent
		if json.Unmarshal([]byte(row.Content), &parsed) != nil {
			continue
		}
		if strings.TrimSpace(parsed.Content) == "" || strings.TrimSpace(parsed.AnchorMessageID) == "" {
			continue
		}
		out = append(out, AttachmentInjection{ID: parsed.ID, AnchorMessageID: parsed.AnchorMessageID, Content: parsed.Content})
	}
	return out, nil
}

// PatchAssistantContent splices updated content into a persisted assistant message. When turnID is
// set only that turn's assistant row is updated; otherwise the latest assistant row for the message.
func (r *ChatAttachmentRepo) PatchAssistantContent(ctx context.Context, sc serverutil.Scope, sessionID, messageID, content, turnID string) error {
	sessionID = strings.TrimSpace(sessionID)
	messageID = strings.TrimSpace(messageID)
	turnID = strings.TrimSpace(turnID)
	if sc.TenantID == "" || sessionID == "" || messageID == "" {
		return ErrAttachmentInvalidArgs
	}

	var res *gorm.DB
	if turnID != "" {
		res = r.db.WithContext(ctx).Model(&ChatMessageRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND message_id = ? AND role = ? AND turn_id = ?",
				sc.TenantID, sc.ScopeID, sessionID, messageID, "assistant", turnID).
			Update("content", content)
	} else {
		res = r.db.WithContext(ctx).Exec(
			`UPDATE llmd_chat_messages SET content = ?
			 WHERE id = (
			   SELECT id FROM llmd_chat_messages
			   WHERE tenant_id = ? AND scope_id = ? AND session_id = ? AND message_id = ? AND role = 'assistant'
			   ORDER BY id DESC LIMIT 1
			 )`,
			content, sc.TenantID, sc.ScopeID, sessionID, messageID)
	}
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAttachmentMessageNotFound
	}
	return nil
}

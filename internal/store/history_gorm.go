package store

import (
	"context"
	"encoding/json"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"

	"github.com/google/uuid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/history"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// History implements history.Store backed by GORM (SQLite or Postgres).
type History struct {
	db *gorm.DB
	sc serverutil.Scope
}

var _ history.Store = (*History)(nil)

// NewHistory creates a chat history store scoped to a single tenant scope (sc).
// sessionID anchors the store to a conversation; sc.TenantID and sessionID must be
// non-empty after trimming, otherwise returns nil (callers should treat as a bug).
func NewHistory(db *gorm.DB, sc serverutil.Scope, sessionID string) *History {
	if db == nil {
		return nil
	}
	sc.TenantID = strings.TrimSpace(sc.TenantID)
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return nil
	}
	return &History{db: db, sc: sc}
}

func (h *History) scope(db *gorm.DB, sessionID string) *gorm.DB {
	return scoped(db, h.sc).Where("session_id = ?", sessionID)
}

func (h *History) Store(sessionID string, message *llmv1.Message) {
	h.StoreInTurn(sessionID, 0, message)
}

// StoreInTurn persists a message, stamping every produced row with the given
// session-scoped turn_seq (llmd's monotonic turn identity) so tool results and
// the assistant/user rows of the same turn share one turn attribution. A
// turnSeq <= 0 leaves turn_seq NULL — for callers with no known turn (e.g. bulk
// history replace). This is a concrete-type method (not on the history.Store
// interface); callers type-assert *History to opt in.
func (h *History) StoreInTurn(sessionID string, turnSeq int64, message *llmv1.Message) {
	if h == nil || h.db == nil || sessionID == "" || message == nil {
		return
	}
	rows := chatRowsFromMessage(h.sc, sessionID, message)
	if len(rows) == 0 {
		return
	}
	for i := range rows {
		if turnSeq > 0 {
			seq := turnSeq
			rows[i].TurnSeq = &seq
		}
	}
	// All rows of one message land atomically: readers that slice history by row
	// id (e.g. SessionRepo.ForkSession) must never observe half a message.
	err := h.db.Transaction(func(tx *gorm.DB) error {
		for i := range rows {
			if err := tx.Create(&rows[i]).Error; err != nil {
				return err
			}
		}
		return refreshSessionLastMessageAt(tx, h.sc, sessionID)
	})
	if err != nil {
		llmdlog.Stdout("history", llmdlog.LevelWarn,
			"persist message failed: %s session=%s message_id=%s role=%s rows=%d: %v",
			h.sc, sessionID, message.GetId(), message.GetRole(), len(rows), err)
	}
}

// Clear deletes all persisted chat rows for a session within this workspace.
func (h *History) Clear(sessionID string) {
	if h == nil || h.db == nil || sessionID == "" {
		return
	}
	h.deleteAndRefresh(sessionID, "", "clear")
}

// deleteAndRefresh deletes a session's chat rows (all, or one message id) and
// recomputes the session's last_message_at in the same transaction.
func (h *History) deleteAndRefresh(sessionID, messageID, op string) {
	err := h.db.Transaction(func(tx *gorm.DB) error {
		q := h.scope(tx, sessionID)
		if messageID != "" {
			q = q.Where("message_id = ?", messageID)
		}
		if err := q.Delete(&ChatMessageRow{}).Error; err != nil {
			return err
		}
		return refreshSessionLastMessageAt(tx, h.sc, sessionID)
	})
	if err != nil {
		llmdlog.Stdout("history", llmdlog.LevelWarn,
			"%s failed: %s session=%s message_id=%s: %v", op, h.sc, sessionID, messageID, err)
	}
}

// Remove deletes all chat rows belonging to a single message id within a session.
func (h *History) Remove(sessionID string, messageID string) {
	if h == nil || h.db == nil || sessionID == "" || messageID == "" {
		return
	}
	h.deleteAndRefresh(sessionID, messageID, "remove")
}

// SessionIDs returns the distinct session ids with persisted chat rows for this workspace.
func (h *History) SessionIDs() []string {
	if h == nil || h.db == nil {
		return nil
	}
	var ids []string
	if err := h.db.Model(&ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ?", h.sc.TenantID, h.sc.ScopeID).
		Distinct().Pluck("session_id", &ids).Error; err != nil {
		return nil
	}
	return ids
}

// TokenCount returns a rough token estimate for a session's persisted content.
func (h *History) TokenCount(sessionID string) int64 {
	if h == nil || h.db == nil || sessionID == "" {
		return 0
	}
	var rows []ChatMessageRow
	if err := h.scope(h.db, sessionID).
		Select("content", "tool_calls_json").
		Find(&rows).Error; err != nil {
		return 0
	}
	var n int64
	for i := range rows {
		n += roughChatTokens(rows[i].Content)
		n += roughChatTokens(rows[i].ToolCallsJSON)
	}
	return n
}

// roughChatTokens is a cheap ~4-chars-per-token heuristic for Latin text.
func roughChatTokens(s string) int64 {
	if s == "" {
		return 0
	}
	if est := int64(len(s) / 4); est > 0 {
		return est
	}
	return 1
}

func chatRowsFromMessage(sc serverutil.Scope, sessionID string, message *llmv1.Message) []ChatMessageRow {
	role := strings.TrimSpace(message.GetRole())
	switch role {
	case "user":
		return []ChatMessageRow{singleRow(sc, sessionID, uuid.NewString(), ChatMsgKindUser, "user", message)}
	case "system":
		return []ChatMessageRow{singleRow(sc, sessionID, uuid.NewString(), ChatMsgKindSystem, "system", message)}
	case "tool":
		turnID := uuid.NewString()
		return []ChatMessageRow{{
			TenantID: sc.TenantID, ScopeID: sc.ScopeID,
			SessionID:  sessionID,
			TurnID:     turnID,
			Kind:       ChatMsgKindToolResult,
			Role:       "tool",
			MessageID:  message.GetId(),
			Content:    message.GetContent(),
			ToolCallID: message.GetToolCallId(),
		}}
	case "assistant":
		return buildAssistantTurnRows(sc, sessionID, "assistant", message)
	default:
		return buildAssistantTurnRows(sc, sessionID, role, message)
	}
}

func singleRow(sc serverutil.Scope, sessionID, turnID, kind, role string, message *llmv1.Message) ChatMessageRow {
	return ChatMessageRow{
		TenantID: sc.TenantID, ScopeID: sc.ScopeID,
		SessionID: sessionID,
		TurnID:    turnID,
		Kind:      kind,
		Role:      role,
		MessageID: message.GetId(),
		Content:   message.GetContent(),
	}
}

func buildAssistantTurnRows(sc serverutil.Scope, sessionID, role string, message *llmv1.Message) []ChatMessageRow {
	turnID := uuid.NewString()
	msgID := message.GetId()
	mk := func(kind string) ChatMessageRow {
		return ChatMessageRow{
			TenantID: sc.TenantID, ScopeID: sc.ScopeID,
			SessionID: sessionID,
			TurnID:    turnID,
			Kind:      kind,
			Role:      role,
			MessageID: msgID,
		}
	}
	var out []ChatMessageRow
	if s := strings.TrimSpace(message.GetThinkingContent()); s != "" {
		r := mk(ChatMsgKindThinking)
		r.Content = s
		out = append(out, r)
	}
	if ah := strings.TrimSpace(message.GetActivityHeadline()); ah != "" {
		r := mk(ChatMsgKindActivity)
		r.Content = ah
		out = append(out, r)
	}
	if c := message.GetContent(); c != "" {
		r := mk(ChatMsgKindAssistant)
		r.Content = c
		r.FinishReason = message.GetFinishReason() // Store finish_reason on the content row
		out = append(out, r)
	}
	if tcs := message.GetToolCalls(); len(tcs) > 0 {
		wire := toolCallsToWire(tcs)
		b, err := json.Marshal(wire)
		if err == nil {
			r := mk(ChatMsgKindTool)
			r.ToolCallsJSON = string(b)
			out = append(out, r)
		}
	}
	// Intentionally do not emit a placeholder row when an assistant turn has no
	// content, thinking, activity, or tool calls: an empty turn would surface as a
	// blank "assistant" bubble in chat and an empty message in model context.
	return out
}

type toolCallWire struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ArgumentsJSON    string `json:"arguments_json"`
	ThoughtSignature []byte `json:"thought_signature,omitempty"`
}

func toolCallsToWire(in []*llmv1.ToolCall) []toolCallWire {
	var out []toolCallWire
	for _, tc := range in {
		if tc == nil {
			continue
		}
		out = append(out, toolCallWire{
			ID:               tc.GetId(),
			Name:             tc.GetName(),
			ArgumentsJSON:    tc.GetArgumentsJson(),
			ThoughtSignature: append([]byte(nil), tc.GetThoughtSignature()...),
		})
	}
	return out
}

func toolCallsFromWire(data []byte) []*llmv1.ToolCall {
	var wire []toolCallWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil
	}
	var out []*llmv1.ToolCall
	for _, w := range wire {
		out = append(out, &llmv1.ToolCall{
			Id:               w.ID,
			Name:             w.Name,
			ArgumentsJson:    w.ArgumentsJSON,
			ThoughtSignature: append([]byte(nil), w.ThoughtSignature...),
		})
	}
	return out
}

func (h *History) History(sessionID string, maxLength int) []*llmv1.Message {
	if h == nil || h.db == nil || sessionID == "" {
		return nil
	}
	out, err := h.loadMerged(context.Background(), sessionID, maxLength, mergeChatRowsToMessages)
	if err != nil {
		llmdlog.Stdout("store", llmdlog.LevelWarn, "history: load failed: %s session=%q err=%v", h.sc, sessionID, err)
		return nil
	}
	return out
}

// historyTailRowsPerMessage seeds the tail window: an assistant message spans
// up to ~4 rows (thinking, activity, content, tool calls).
const historyTailRowsPerMessage = 4

// loadMerged reads a session's chat rows and merges them into messages. With
// maxLength > 0 it reads only a tail window of rows (newest first), doubling
// it until the window holds maxLength whole messages or reaches the start of
// the session, so a short tail of a long session never loads the whole thing.
func (h *History) loadMerged(ctx context.Context, sessionID string, maxLength int, merge func([]ChatMessageRow) []*llmv1.Message) ([]*llmv1.Message, error) {
	base := func() *gorm.DB {
		return h.db.WithContext(ctx).Model(&ChatMessageRow{}).Where(
			"tenant_id = ? AND scope_id = ? AND session_id = ?",
			h.sc.TenantID, h.sc.ScopeID, sessionID,
		)
	}
	if maxLength <= 0 {
		var rows []ChatMessageRow
		if err := base().Order("id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		return merge(rows), nil
	}
	limit := maxLength * historyTailRowsPerMessage
	for {
		var rows []ChatMessageRow
		if err := base().Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
			return nil, err
		}
		reachedStart := len(rows) < limit
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
		if !reachedStart {
			rows = dropLeadingPartialTurn(rows)
		}
		out := merge(rows)
		if len(out) >= maxLength || reachedStart {
			if len(out) > maxLength {
				out = out[len(out)-maxLength:]
			}
			return out, nil
		}
		limit *= 2
	}
}

// dropLeadingPartialTurn removes the assistant-turn rows at the head of a
// truncated window: their turn may continue before the window, so merging them
// would produce half a message.
func dropLeadingPartialTurn(rows []ChatMessageRow) []ChatMessageRow {
	if len(rows) == 0 || !isAssistantTurnPart(rows[0].Kind) {
		return rows
	}
	turn := rows[0].TurnID
	i := 0
	for i < len(rows) && rows[i].TurnID == turn && isAssistantTurnPart(rows[i].Kind) {
		i++
	}
	return rows[i:]
}

// turnUsageIndex finds the llmd_turn_metadata row for an assistant message.
// Rows written since the message_id column was added match on the message id;
// the chat rows' turn_id is minted separately and does not equal the metadata
// row's turn_id for those. Legacy rows without a message id match on turn_id.
type turnUsageIndex struct {
	byMessage map[string]*TurnMetadataRow
	byTurn    map[string]*TurnMetadataRow
}

func newTurnUsageIndex(rows []TurnMetadataRow) turnUsageIndex {
	ix := turnUsageIndex{byMessage: map[string]*TurnMetadataRow{}, byTurn: map[string]*TurnMetadataRow{}}
	for i := range rows {
		r := &rows[i]
		if r.MessageID != "" {
			ix.byMessage[r.MessageID] = r
		} else if r.TurnID != "" {
			ix.byTurn[r.TurnID] = r
		}
	}
	return ix
}

func (ix turnUsageIndex) lookup(messageID, turnID string) *TurnMetadataRow {
	if messageID != "" {
		if r, ok := ix.byMessage[messageID]; ok {
			return r
		}
	}
	if turnID != "" {
		if r, ok := ix.byTurn[turnID]; ok {
			return r
		}
	}
	return nil
}

// tokenUsageFromMeta copies a metadata row's provider token counts into a
// TokenUsage; nil when the row recorded no counts at all.
func tokenUsageFromMeta(r *TurnMetadataRow) *llmv1.TokenUsage {
	if r.ProviderInToks == nil && r.ProviderOutToks == nil && r.ProviderCacheReadToks == nil && r.ProviderCacheWriteToks == nil {
		return nil
	}
	return &llmv1.TokenUsage{
		InputTokens:      copyInt64(r.ProviderInToks),
		OutputTokens:     copyInt64(r.ProviderOutToks),
		CacheReadTokens:  copyInt64(r.ProviderCacheReadToks),
		CacheWriteTokens: copyInt64(r.ProviderCacheWriteToks),
	}
}

func copyInt64(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// HistoryWithUsage is like History but also populates Message.Usage and
// Message.ModelDurationMs from llmd_turn_metadata for assistant turns. Pass a
// TurnMetadataRepo to enable the join; nil repo falls back to plain merge.
func (h *History) HistoryWithUsage(ctx context.Context, sessionID string, maxLength int, meta *TurnMetadataRepo) []*llmv1.Message {
	if h == nil || h.db == nil || sessionID == "" {
		return nil
	}
	var metaRows []TurnMetadataRow
	if meta != nil {
		var merr error
		metaRows, merr = meta.ListBySession(ctx, h.sc, sessionID)
		if merr != nil {
			llmdlog.Stdout("store", llmdlog.LevelWarn, "history: list turn metadata failed: %s session=%q err=%v", h.sc, sessionID, merr)
		}
	}
	usage := newTurnUsageIndex(metaRows)
	out, err := h.loadMerged(ctx, sessionID, maxLength, func(rows []ChatMessageRow) []*llmv1.Message {
		return mergeChatRowsToMessagesWithUsage(rows, usage)
	})
	if err != nil {
		llmdlog.Stdout("store", llmdlog.LevelWarn, "history: load failed: %s session=%q err=%v", h.sc, sessionID, err)
		return nil
	}
	return out
}

func isAssistantTurnPart(kind string) bool {
	switch kind {
	case ChatMsgKindThinking, ChatMsgKindActivity, ChatMsgKindAssistant, ChatMsgKindTool:
		return true
	default:
		return false
	}
}

// assistantTurnMessageIsEmpty reports whether a reconstructed assistant turn carries
// nothing worth sending to the model or showing in chat (no visible content, tool
// calls, thinking, or activity). Such placeholder turns are skipped during merge so
// they never reach the LLM context or render as blank "assistant" bubbles.
func assistantTurnMessageIsEmpty(m *llmv1.Message) bool {
	if m == nil {
		return true
	}
	if strings.TrimSpace(m.GetContent()) != "" {
		return false
	}
	if len(m.GetToolCalls()) > 0 {
		return false
	}
	if strings.TrimSpace(m.GetThinkingContent()) != "" {
		return false
	}
	if strings.TrimSpace(m.GetActivityHeadline()) != "" {
		return false
	}
	return true
}

// StoreCaptured persists provider-internal tool messages (Claude Code / Cursor inner loop
// rounds) under the captured_assistant / captured_tool_result kinds.  These rows are
// intentionally excluded from History() — they show up in the diagnostic context view but
// never pollute the LLM context sent on subsequent turns.
func (h *History) StoreCaptured(sessionID string, turnSeq int64, msgs []*llmv1.Message) {
	if h == nil || h.db == nil || sessionID == "" {
		return
	}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		var rows []ChatMessageRow
		switch strings.TrimSpace(m.GetRole()) {
		case "assistant":
			rows = buildCapturedAssistantRow(h.sc, sessionID, m)
		case "tool":
			rows = []ChatMessageRow{buildCapturedToolResultRow(h.sc, sessionID, m)}
		}
		for i := range rows {
			// Stamp the turn that captured these provider-internal rows so they carry
			// the same turn_seq as the turn's user/assistant rows and its decorations.
			if turnSeq > 0 {
				seq := turnSeq
				rows[i].TurnSeq = &seq
			}
			if err := h.db.Create(&rows[i]).Error; err != nil {
				llmdlog.Stdout("history", llmdlog.LevelWarn,
					"persist captured row failed: %s session=%s: %v", h.sc, sessionID, err)
				continue
			}
			if err := refreshSessionLastMessageAt(h.db, h.sc, sessionID); err != nil {
				llmdlog.Stdout("history", llmdlog.LevelWarn,
					"refresh last_message_at failed: %s session=%s: %v", h.sc, sessionID, err)
			}
		}
	}
}

func buildCapturedAssistantRow(sc serverutil.Scope, sessionID string, m *llmv1.Message) []ChatMessageRow {
	turnID := uuid.NewString()
	msgID := m.GetId()
	var out []ChatMessageRow
	if c := m.GetContent(); c != "" {
		out = append(out, ChatMessageRow{
			TenantID: sc.TenantID, ScopeID: sc.ScopeID,
			SessionID: sessionID,
			TurnID:    turnID,
			Kind:      ChatMsgKindCapturedAssistant,
			Role:      "assistant",
			MessageID: msgID,
			Content:   c,
		})
	}
	return out
}

func buildCapturedToolResultRow(sc serverutil.Scope, sessionID string, m *llmv1.Message) ChatMessageRow {
	return ChatMessageRow{
		TenantID: sc.TenantID, ScopeID: sc.ScopeID,
		SessionID:  sessionID,
		TurnID:     uuid.NewString(),
		Kind:       ChatMsgKindCapturedToolResult,
		Role:       "tool",
		MessageID:  m.GetId(),
		Content:    m.GetContent(),
		ToolCallID: m.GetToolCallId(),
	}
}

func mergeChatRowsToMessages(rows []ChatMessageRow) []*llmv1.Message {
	var out []*llmv1.Message
	for i := 0; i < len(rows); {
		r := rows[i]
		switch r.Kind {
		case ChatMsgKindUser, ChatMsgKindSystem:
			out = append(out, cloneProtoMessage(rowToProtoMessage(&r)))
			i++
		case ChatMsgKindToolResult:
			out = append(out, cloneProtoMessage(rowToProtoMessage(&r)))
			i++
		case ChatMsgKindThinking, ChatMsgKindActivity, ChatMsgKindAssistant, ChatMsgKindTool:
			turn := r.TurnID
			msgID := r.MessageID
			role := r.Role
			if role == "" {
				role = "assistant"
			}
			var m llmv1.Message
			m.Role = role
			m.Id = msgID
			for i < len(rows) && rows[i].TurnID == turn && isAssistantTurnPart(rows[i].Kind) {
				switch rows[i].Kind {
				case ChatMsgKindThinking:
					if m.ThinkingContent != "" {
						m.ThinkingContent += "\n"
					}
					m.ThinkingContent += rows[i].Content
				case ChatMsgKindActivity:
					if m.ActivityHeadline != "" {
						m.ActivityHeadline += "\n"
					}
					m.ActivityHeadline += rows[i].Content
				case ChatMsgKindAssistant:
					m.Content = rows[i].Content
					// Restore finish_reason from the assistant content row
					if rows[i].FinishReason != "" {
						m.FinishReason = rows[i].FinishReason
					}
				case ChatMsgKindTool:
					m.ToolCalls = append(m.ToolCalls, toolCallsFromWire([]byte(rows[i].ToolCallsJSON))...)
				}
				i++
			}
			if !assistantTurnMessageIsEmpty(&m) {
				out = append(out, cloneProtoMessage(&m))
			}
		// Captured provider-internal rows are intentionally excluded from LLM history.
		case ChatMsgKindCapturedAssistant, ChatMsgKindCapturedToolResult:
			i++
		default:
			i++
		}
	}
	return out
}

// mergeChatRowsToMessagesWithUsage is mergeChatRowsToMessages augmented with
// per-turn token usage and model time.
func mergeChatRowsToMessagesWithUsage(rows []ChatMessageRow, usage turnUsageIndex) []*llmv1.Message {
	var out []*llmv1.Message
	for i := 0; i < len(rows); {
		r := rows[i]
		switch r.Kind {
		case ChatMsgKindUser, ChatMsgKindSystem:
			out = append(out, cloneProtoMessage(rowToProtoMessage(&r)))
			i++
		case ChatMsgKindToolResult:
			out = append(out, cloneProtoMessage(rowToProtoMessage(&r)))
			i++
		case ChatMsgKindThinking, ChatMsgKindActivity, ChatMsgKindAssistant, ChatMsgKindTool:
			turn := r.TurnID
			msgID := r.MessageID
			role := r.Role
			if role == "" {
				role = "assistant"
			}
			var m llmv1.Message
			m.Role = role
			m.Id = msgID
			for i < len(rows) && rows[i].TurnID == turn && isAssistantTurnPart(rows[i].Kind) {
				switch rows[i].Kind {
				case ChatMsgKindThinking:
					if m.ThinkingContent != "" {
						m.ThinkingContent += "\n"
					}
					m.ThinkingContent += rows[i].Content
				case ChatMsgKindActivity:
					if m.ActivityHeadline != "" {
						m.ActivityHeadline += "\n"
					}
					m.ActivityHeadline += rows[i].Content
				case ChatMsgKindAssistant:
					m.Content = rows[i].Content
					// Restore finish_reason from the assistant content row
					if rows[i].FinishReason != "" {
						m.FinishReason = rows[i].FinishReason
					}
				case ChatMsgKindTool:
					m.ToolCalls = append(m.ToolCalls, toolCallsFromWire([]byte(rows[i].ToolCallsJSON))...)
				}
				i++
			}
			if mr := usage.lookup(msgID, turn); mr != nil {
				m.Usage = tokenUsageFromMeta(mr)
				m.ModelDurationMs = copyInt64(mr.ModelDurationMs)
			}
			if !assistantTurnMessageIsEmpty(&m) {
				out = append(out, cloneProtoMessage(&m))
			}
		// Captured provider-internal rows are intentionally excluded from LLM history.
		case ChatMsgKindCapturedAssistant, ChatMsgKindCapturedToolResult:
			i++
		default:
			i++
		}
	}
	return out
}

// rowToProtoMessage rebuilds a single-row message (user, system, tool result).
// ToolCallId MUST round-trip: a tool result without it cannot be paired with
// the assistant tool call it answers, and providers reject (or misattribute)
// unpaired tool messages when history is replayed.
func rowToProtoMessage(r *ChatMessageRow) *llmv1.Message {
	return &llmv1.Message{
		Id:         r.MessageID,
		Role:       r.Role,
		Content:    r.Content,
		ToolCallId: r.ToolCallID,
	}
}

func cloneProtoMessage(m *llmv1.Message) *llmv1.Message {
	if m == nil {
		return nil
	}
	return proto.Clone(m).(*llmv1.Message)
}

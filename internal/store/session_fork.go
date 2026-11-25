package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Sentinel errors for ForkSession, so the RPC layer can map them to gRPC codes.
var (
	ErrForkSourceNotFound  = errors.New("fork: source session not found")
	ErrForkMessageNotFound = errors.New("fork: up_to_message_id not found in source session")
	ErrForkTargetExists    = errors.New("fork: target session already exists")
	ErrForkTurnInProgress  = errors.New("fork: cloned range reaches into a turn that is still running")
)

// ForkOptions carries the caller-supplied knobs for ForkSession.
type ForkOptions struct {
	// Title overrides the copied session title when non-empty.
	Title string
	// InProgressTurnSeq, when > 0, is the turn_seq of a turn currently running
	// on the source. The fork is refused (ErrForkTurnInProgress) if any copied
	// row carries that turn_seq or later: such a turn may still be missing tool
	// results or its final assistant message. Checked inside the fork
	// transaction, before anything is written.
	InProgressTurnSeq int64
}

// ForkSessionResult describes what ForkSession copied. CutoffRowID / MaxTurnSeq
// let the caller verify the copied range does not reach into a turn that is
// still running (the store cannot know that on its own).
type ForkSessionResult struct {
	SessionID      string
	CopiedRows     int
	CopiedMessages int
	// CutoffRowID is the highest llmd_chat_messages.id (in the SOURCE session)
	// that was copied; 0 when nothing was copied.
	CutoffRowID uint64
	// MaxTurnSeq is the highest turn_seq among copied rows; 0 when none carried one.
	MaxTurnSeq int64
}

// forkBoundaryExcludedKinds are rows that never start a new user/agent message:
// tool results belong to the preceding tool round, captured_* rows are
// provider-internal diagnostics, attachments hang off an existing message.
var forkBoundaryExcludedKinds = []string{
	ChatMsgKindToolResult,
	ChatMsgKindCapturedAssistant,
	ChatMsgKindCapturedToolResult,
	ChatMsgKindAttachment,
}

// forkInChunk bounds IN (...) list sizes to stay well under SQLite's bind limit.
const forkInChunk = 500

// ForkSession clones srcSessionID into a new dstSessionID, inclusive of
// upToMessageID. The fork owns a full, independent copy of its data; nothing
// is shared with or read through to the source.
//
// Cutoff rule: find the first user/assistant row AFTER the last row of
// upToMessageID and copy every source row with a lower id. That carries the
// tool results of an assistant message's tool calls along with it, so a fork
// never ends on an unanswered tool call. If no such row exists, everything is
// copied. An empty upToMessageID clones the whole history.
//
// Copied: the session row (with ForkedFrom* set, cursor reset, queue unpaused),
// chat rows, session turns, turn metadata, tool-result sidecars, turn
// decorations, raw turn chunks and tracked-document versions belonging to the
// copied range.
// Not copied: provider session sync (the fork replays as a fresh provider
// session, like a provider/model switch), session events (rebuildable), the
// K/V cache, drafts and the message queue (ephemeral / in-flight state).
func (r *SessionRepo) ForkSession(ctx context.Context, sc serverutil.Scope, srcSessionID, dstSessionID, upToMessageID string, opts ForkOptions) (*ForkSessionResult, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("session repo not configured")
	}
	src := strings.TrimSpace(srcSessionID)
	dst := strings.TrimSpace(dstSessionID)
	upTo := strings.TrimSpace(upToMessageID)
	if sc.TenantID == "" || src == "" || dst == "" {
		return nil, fmt.Errorf("tenant_id, source session_id and target session_id are required")
	}
	if src == dst {
		return nil, fmt.Errorf("fork: target session_id must differ from source")
	}

	res := &ForkSessionResult{SessionID: dst}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var srcRow SessionRow
		if err := tx.Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, src).First(&srcRow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrForkSourceNotFound
			}
			return fmt.Errorf("fork: load source session: %w", err)
		}
		if err := ensureForkTargetFree(tx, sc, dst); err != nil {
			return err
		}

		cutoff, err := forkCutoffRowID(tx, sc, src, upTo)
		if err != nil {
			return err
		}
		res.CutoffRowID = cutoff

		if opts.InProgressTurnSeq > 0 && cutoff > 0 {
			var n int64
			if err := scoped(tx.Model(&ChatMessageRow{}), sc).
				Where("session_id = ? AND id <= ? AND turn_seq >= ?", src, cutoff, opts.InProgressTurnSeq).
				Count(&n).Error; err != nil {
				return fmt.Errorf("fork: check in-progress turn: %w", err)
			}
			if n > 0 {
				return ErrForkTurnInProgress
			}
		}

		// Session row: full copy of settings, fresh identity/timestamps.
		now := time.Now().UTC()
		newRow := srcRow
		newRow.SessionID = dst
		newRow.ForkedFromSessionID = src
		newRow.ForkedFromMessageID = upTo
		newRow.CursorPos = 0
		newRow.QueuePaused = nil
		if t := strings.TrimSpace(opts.Title); t != "" {
			newRow.Title = t
		}
		newRow.CreatedAt = now
		newRow.UpdatedAt = now
		if err := tx.Create(&newRow).Error; err != nil {
			return fmt.Errorf("fork: create session row: %w", err)
		}

		if cutoff == 0 {
			return nil // empty source history: session row only
		}

		var chatRows []ChatMessageRow
		if err := tx.Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND id <= ?", sc.TenantID, sc.ScopeID, src, cutoff).
			Order("id ASC").Find(&chatRows).Error; err != nil {
			return fmt.Errorf("fork: load chat rows: %w", err)
		}
		messageIDs := map[string]struct{}{}
		toolCallIDs := map[string]struct{}{}
		for i := range chatRows {
			row := &chatRows[i]
			if row.MessageID != "" {
				messageIDs[row.MessageID] = struct{}{}
			}
			if row.ToolCallID != "" {
				toolCallIDs[row.ToolCallID] = struct{}{}
			}
			if row.ToolCallsJSON != "" {
				var calls []toolCallWire
				if err := json.Unmarshal([]byte(row.ToolCallsJSON), &calls); err != nil {
					return fmt.Errorf("fork: decode tool_calls_json on row %d: %w", row.ID, err)
				}
				for _, c := range calls {
					if c.ID != "" {
						toolCallIDs[c.ID] = struct{}{}
					}
				}
			}
			if row.TurnSeq != nil && *row.TurnSeq > res.MaxTurnSeq {
				res.MaxTurnSeq = *row.TurnSeq
			}
			row.ID = 0
			row.SessionID = dst
		}
		if len(chatRows) > 0 {
			if err := tx.CreateInBatches(&chatRows, 200).Error; err != nil {
				return fmt.Errorf("fork: copy chat rows: %w", err)
			}
			if err := refreshSessionLastMessageAt(tx, sc, dst); err != nil {
				return fmt.Errorf("fork: refresh last_message_at: %w", err)
			}
		}
		res.CopiedRows = len(chatRows)
		res.CopiedMessages = len(messageIDs)
		msgIDs := setKeys(messageIDs)
		callIDs := setKeys(toolCallIDs)

		// Session turns of this scope.
		turnExternalIDs := map[string]struct{}{}
		if res.MaxTurnSeq > 0 {
			var turns []SessionTurnRow
			if err := tx.Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND turn_seq <= ?", sc.TenantID, sc.ScopeID, src, res.MaxTurnSeq).Find(&turns).Error; err != nil {
				return fmt.Errorf("fork: load session turns: %w", err)
			}
			for i := range turns {
				if turns[i].ExternalTurnID != "" {
					turnExternalIDs[turns[i].ExternalTurnID] = struct{}{}
				}
				turns[i].SessionID = dst
			}
			if len(turns) > 0 {
				if err := tx.CreateInBatches(&turns, 200).Error; err != nil {
					return fmt.Errorf("fork: copy session turns: %w", err)
				}
			}
		}

		// Turn metadata, joined by assistant message id.
		var metas []TurnMetadataRow
		if err := forkFindIn(tx, &metas, "tenant_id = ? AND scope_id = ? AND session_id = ?", []any{sc.TenantID, sc.ScopeID, src}, "message_id", msgIDs); err != nil {
			return fmt.Errorf("fork: load turn metadata: %w", err)
		}
		for i := range metas {
			metas[i].SessionID = dst
			if metas[i].TurnID != "" {
				turnExternalIDs[metas[i].TurnID] = struct{}{}
			}
		}
		if len(metas) > 0 {
			if err := tx.CreateInBatches(&metas, 200).Error; err != nil {
				return fmt.Errorf("fork: copy turn metadata: %w", err)
			}
		}

		// Tool-result sidecars.
		var sidecars []ToolResultSidecarRow
		if err := forkFindIn(tx, &sidecars, "tenant_id = ? AND scope_id = ? AND session_id = ?", []any{sc.TenantID, sc.ScopeID, src}, "tool_call_id", callIDs); err != nil {
			return fmt.Errorf("fork: load tool result sidecars: %w", err)
		}
		for i := range sidecars {
			sidecars[i].ID = 0
			sidecars[i].SessionID = dst
		}
		if len(sidecars) > 0 {
			if err := tx.CreateInBatches(&sidecars, 200).Error; err != nil {
				return fmt.Errorf("fork: copy tool result sidecars: %w", err)
			}
		}

		// Decorations: result-level by tool_call_id, turn-level by turn_seq.
		var decos []TurnDecorationRow
		if err := forkFindIn(tx, &decos, "tenant_id = ? AND scope_id = ? AND session_id = ? AND target <> ?", []any{sc.TenantID, sc.ScopeID, src, "turn"}, "tool_call_id", callIDs); err != nil {
			return fmt.Errorf("fork: load tool result decorations: %w", err)
		}
		if res.MaxTurnSeq > 0 {
			var turnDecos []TurnDecorationRow
			if err := tx.Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND target = ? AND turn_seq IS NOT NULL AND turn_seq <= ?",
				sc.TenantID, sc.ScopeID, src, "turn", res.MaxTurnSeq).Find(&turnDecos).Error; err != nil {
				return fmt.Errorf("fork: load turn decorations: %w", err)
			}
			decos = append(decos, turnDecos...)
		}
		for i := range decos {
			decos[i].ID = 0
			decos[i].SessionID = dst
		}
		if len(decos) > 0 {
			if err := tx.CreateInBatches(&decos, 200).Error; err != nil {
				return fmt.Errorf("fork: copy turn decorations: %w", err)
			}
		}

		// Tracked document versions anchored at copied messages.
		var docs []TrackedDocumentVersionRow
		if err := forkFindIn(tx, &docs, "tenant_id = ? AND scope_id = ? AND session_id = ?", []any{sc.TenantID, sc.ScopeID, src}, "anchor_message_id", msgIDs); err != nil {
			return fmt.Errorf("fork: load tracked document versions: %w", err)
		}
		for i := range docs {
			docs[i].ID = 0
			docs[i].SessionID = dst
		}
		if len(docs) > 0 {
			if err := tx.CreateInBatches(&docs, 200).Error; err != nil {
				return fmt.Errorf("fork: copy tracked document versions: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ensureForkTargetFree refuses to fork onto a session id that already has a
// session row or any chat rows, so a fork never merges into existing data.
func ensureForkTargetFree(tx *gorm.DB, sc serverutil.Scope, dst string) error {
	var n int64
	if err := tx.Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, dst).Count(&n).Error; err != nil {
		return fmt.Errorf("fork: check target session: %w", err)
	}
	if n > 0 {
		return ErrForkTargetExists
	}
	if err := tx.Model(&ChatMessageRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, dst).Count(&n).Error; err != nil {
		return fmt.Errorf("fork: check target chat rows: %w", err)
	}
	if n > 0 {
		return ErrForkTargetExists
	}
	return nil
}

// forkCutoffRowID returns the highest source row id to copy (inclusive), or 0
// when the source has no history.
func forkCutoffRowID(tx *gorm.DB, sc serverutil.Scope, src, upTo string) (uint64, error) {
	base := func() *gorm.DB {
		return tx.Model(&ChatMessageRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, src)
	}
	var maxAll *uint64
	if err := base().Select("MAX(id)").Scan(&maxAll).Error; err != nil {
		return 0, fmt.Errorf("fork: find last row: %w", err)
	}
	if upTo == "" {
		if maxAll == nil {
			return 0, nil
		}
		return *maxAll, nil
	}
	var lastOfMsg *uint64
	if err := base().Where("message_id = ?", upTo).Select("MAX(id)").Scan(&lastOfMsg).Error; err != nil {
		return 0, fmt.Errorf("fork: find message rows: %w", err)
	}
	if lastOfMsg == nil {
		return 0, ErrForkMessageNotFound
	}
	var next *uint64
	if err := base().
		Where("id > ? AND role IN ? AND kind NOT IN ?", *lastOfMsg, []string{"user", "assistant"}, forkBoundaryExcludedKinds).
		Select("MIN(id)").Scan(&next).Error; err != nil {
		return 0, fmt.Errorf("fork: find next message boundary: %w", err)
	}
	if next == nil {
		return *maxAll, nil
	}
	return *next - 1, nil
}

// forkFindIn loads rows matching baseWhere AND col IN (vals), chunking vals.
func forkFindIn[T any](tx *gorm.DB, out *[]T, baseWhere string, baseArgs []any, col string, vals []string) error {
	for start := 0; start < len(vals); start += forkInChunk {
		end := start + forkInChunk
		if end > len(vals) {
			end = len(vals)
		}
		var part []T
		args := append(append([]any{}, baseArgs...), vals[start:end])
		if err := tx.Where(baseWhere+" AND "+col+" IN ?", args...).Find(&part).Error; err != nil {
			return err
		}
		*out = append(*out, part...)
	}
	return nil
}

func setKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

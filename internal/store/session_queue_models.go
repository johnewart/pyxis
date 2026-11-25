package store

import (
	"encoding/json"
	"strings"
	"time"
)

// SessionDraftRow is a session's single unsent composer draft.
//
// Exactly one per session, enforced by the primary key rather than by
// application code: a draft is "what is currently typed into this session's
// composer", and that is a single value by definition. Its whole purpose is to
// outlive the UI holding it — a reload, a crash, a closed laptop — so it is
// deliberately cheap to write (one upsert per keystroke-ish, debounced by the
// caller) and cheap to throw away (deleted the moment the message is actually
// sent).
//
// Not history: a draft has never been sent to a model and must never appear in
// llmd_chat_messages or in any transcript the model sees.
type SessionDraftRow struct {
	TenantID  string `gorm:"column:tenant_id;primaryKey;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;primaryKey;size:256;not null;default:''"`
	SessionID string `gorm:"primaryKey;size:256;not null"`
	Content   string `gorm:"type:text"`
	UpdatedAt time.Time
}

func (SessionDraftRow) TableName() string { return "llmd_session_drafts" }

// QueuedMessageState is the lifecycle of one queued message.
//
// Only Pending rows are waiting to run. Running marks an entry some drainer has
// claimed, so a second drainer cannot claim it too; the row is deleted once its
// turn is committed. There is deliberately no terminal "done" state kept
// around: the turn itself becomes the record, in history, and leaving spent
// queue rows behind would turn the queue into a second, diverging transcript.
type QueuedMessageState string

const (
	QueuedPending QueuedMessageState = "pending"
	QueuedRunning QueuedMessageState = "running"
)

// SessionQueuedMessageRow is one user message waiting its turn on a session.
//
// Ordering is by Position, not by CreatedAt, because the queue is explicitly
// reorderable — the user can promote a message they decide matters more. New
// entries land at the back (max(position)+1). Reordering rewrites positions
// rather than swapping rows, so the order is always a total order with no ties
// to break at read time.
//
// Position is a float64 so an entry can be inserted between two neighbours
// without renumbering the whole queue. ReorderQueue does renumber (it is given
// the full desired order), but promote/demote-style edits stay O(1).
type SessionQueuedMessageRow struct {
	// ID is the queue entry's own identity — what remove/reorder address. It is
	// NOT the message id: the message id is minted when the entry is finally
	// sent as a turn, because until then it is not a message in any transcript.
	ID        string  `gorm:"primaryKey;size:64;not null"`
	TenantID  string  `gorm:"column:tenant_id;index:idx_session_queue_lookup,priority:1;size:256;not null"`
	ScopeID   string  `gorm:"column:scope_id;index:idx_session_queue_lookup,priority:2;size:256;not null;default:''"`
	SessionID string  `gorm:"index:idx_session_queue_lookup,priority:3;size:256;not null"`
	Position  float64 `gorm:"index:idx_session_queue_lookup,priority:4;not null"`
	Content   string  `gorm:"type:text"`
	State     string  `gorm:"size:32;not null"`

	// ClaimOwner identifies the drainer currently running this entry (the value
	// it passed to ClaimNext). Empty while pending. Drainers are ordinary
	// clients — a host process looping in the background, a browser tab while it is
	// open — and several can be alive at once, so "who holds this" has to be
	// recorded rather than assumed.
	ClaimOwner string `gorm:"column:claim_owner;size:128"`
	// ClaimExpiresAt is when this claim stops being believed. A drainer that
	// dies mid-turn cannot release its claim, and without an expiry the entry
	// would stay Running forever and wedge the queue permanently — the exact
	// failure this feature exists to survive. The owner heartbeats to push this
	// forward for as long as its turn is genuinely still streaming; once it is
	// in the past, any drainer may reclaim the entry.
	ClaimExpiresAt *time.Time `gorm:"column:claim_expires_at;index"`
	// RanTurnID records the turn a claim actually started, and is what makes
	// delivery at-most-once rather than at-least-once. A drainer that claims an
	// entry, starts the turn, then dies before retiring it would otherwise be
	// reclaimed and re-sent, duplicating the user's message. Set when the
	// drainer reports the turn it minted; a reclaim that finds this non-empty
	// must not run the entry again.
	RanTurnID string `gorm:"column:ran_turn_id;size:128"`

	// AttachmentsJSON is the JSON-encoded []QueuedAttachment the message will
	// carry when sent. Nullable/empty for the common no-attachment case, so it
	// is additive for existing databases (AutoMigrate adds the column).
	AttachmentsJSON string `gorm:"column:attachments_json;type:text"`

	// DisableTools runs this entry's turn with no tools (TurnOptions.disable_tools).
	// Defaulted false, so additive for existing databases.
	DisableTools bool `gorm:"column:disable_tools;not null;default:false"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (SessionQueuedMessageRow) TableName() string { return "llmd_session_queue" }

// QueuedTurnOptions are per-turn switches stored with a queued entry and
// applied to the turn it starts (mirrors llmv1.TurnOptions).
type QueuedTurnOptions struct {
	DisableTools bool
}

// TurnOptions returns the entry's per-turn switches.
func (r SessionQueuedMessageRow) TurnOptions() QueuedTurnOptions {
	return QueuedTurnOptions{DisableTools: r.DisableTools}
}

// QueuedAttachment is one attachment carried by a queued message (mirrors
// llmv1.MessageAttachment; kept proto-free so the store does not import gen).
type QueuedAttachment struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	PayloadJSON     string `json:"payload_json,omitempty"`
	AfterToolCallID string `json:"after_tool_call_id,omitempty"`
	Content         string `json:"content,omitempty"`
}

// Attachments decodes AttachmentsJSON. Empty/absent decodes to nil.
func (r SessionQueuedMessageRow) Attachments() ([]QueuedAttachment, error) {
	if strings.TrimSpace(r.AttachmentsJSON) == "" {
		return nil, nil
	}
	var out []QueuedAttachment
	if err := json.Unmarshal([]byte(r.AttachmentsJSON), &out); err != nil {
		return nil, err
	}
	return out, nil
}

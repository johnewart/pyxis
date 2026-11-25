package store

import (
	"time"

	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// SessionRow persists chat session runtime metadata (Tier 2 session-first storage).
type SessionRow struct {
	TenantID       string `gorm:"column:tenant_id;primaryKey;index:idx_sessions_updated,priority:1;index:idx_sessions_surface_updated,priority:1;size:256;not null"`
	ScopeID        string `gorm:"column:scope_id;primaryKey;index:idx_sessions_updated,priority:2;index:idx_sessions_surface_updated,priority:2;size:256;not null;default:''"`
	SessionID      string `gorm:"primaryKey;size:256"`
	ProviderID     string `gorm:"size:128"`
	DefaultModel   string `gorm:"size:256"`
	ToolsJSON      string `gorm:"type:text"`
	Title          string `gorm:"size:512"`
	TodosJSON      string `gorm:"type:text"`
	AttributesJSON string `gorm:"type:text"`
	// ChatSurface is the session's UI surface / agent type (build, ask, debug,
	// doc, pair). First-class column so the session list can filter by surface
	// without decoding attributes_json.
	ChatSurface string `gorm:"size:64;index:idx_sessions_surface_updated,priority:3"`
	// TagsJSON holds arbitrary caller-defined k/v labels as a flat JSON object
	// of string→string (`{"team":"infra","env":"prod"}`). Empty string means "no
	// tags" — NOT "{}" — so a row written before this column existed reads back
	// as untagged without a backfill.
	//
	// Distinct from AttributesJSON, which is an opaque blob llmd never looks
	// inside. Tags are queryable: ListSessions filters on them with per-dialect
	// JSON extraction (see session_tags.go). The tradeoff is that filtering
	// scans the scope's sessions rather than seeking an index — fine at the
	// hundreds-to-thousands per scope this is built for. If tag filtering
	// ever needs to scale past that, the fix is a side table keyed
	// (tenant_id, scope_id, key, value), not an index on this column.
	TagsJSON  string `gorm:"column:tags_json;type:text"`
	CursorPos int64  `gorm:"default:0"`
	// InitialSeed is the session-scoped, first-message context block (graph seed,
	// neighborhood overview, etc.) rendered once at session start and injected —
	// frozen — as the lead context on the first outbound model message. Single-shot
	// and unversioned; empty when no seed applies. Storing it on the session (rather
	// than folding it into the first user message) keeps the persisted user content
	// exactly what the user typed.
	InitialSeed string `gorm:"column:initial_seed;type:text"`
	// SystemPromptSectionsJSON is the session-level default structured system
	// prompt (see llm.v1.EnsureSessionRequest.system_prompt_sections): set once
	// via EnsureSession instead of resent on every ChatStreamRequest. A
	// ChatStreamRequest that carries its own system_prompt_sections still
	// overrides this default for that turn only; this column is untouched
	// either way. Empty when no session default has been set.
	SystemPromptSectionsJSON string `gorm:"column:system_prompt_sections_json;type:text"`
	// DisabledToolsJSON is the set of server-wide tool names hidden from this
	// session's model-facing tool list by default (see
	// llm.v1.EnsureSessionRequest.disabled_tools). Empty string = no exclusions:
	// rows written before this column existed read back as "none" with no
	// backfill. Non-empty = a JSON array of tool names (see
	// MarshalDisabledToolsJSON / UnmarshalDisabledToolsJSON). Deny wins: a name
	// listed here is hidden AND refused at dispatch, and per-session
	// registration of the same name does not undo it. Only core tools are
	// exempt.
	DisabledToolsJSON string `gorm:"column:disabled_tools_json;type:text"`
	// ToolPolicy decides the fate of server-wide tools this session neither
	// registered nor disabled (see llm.v1.EnsureSessionRequest.tool_policy).
	// Empty string = inherit server-wide, so rows written before this column
	// existed keep today's behaviour with no backfill. "profile_only" = the
	// session sees only core tools and the tools it registered itself, which is
	// what stops a tool registered server-wide LATER from appearing in a session
	// created before it. See toolmerge.ParsePolicy for the parse (unrecognised
	// values fail open to inherit).
	ToolPolicy string `gorm:"column:tool_policy;size:32"`
	// ToolContextJSON is the session's default tool_context map (see
	// llm.v1.EnsureSessionRequest.tool_context) as a JSON object. Empty = none;
	// older rows read back as none. A ChatStreamRequest tool_context overrides
	// it for that turn. Added by GORM AutoMigrate (nullable text column).
	ToolContextJSON string `gorm:"column:tool_context_json;type:text"`
	// Compression settings (per-session). ToolResultCompressionEnabled is the master
	// switch: when NULL/false the session does not compress, regardless of the
	// mechanism flags below (which are still persisted so they survive a toggle).
	ToolResultCompressionEnabled *bool `gorm:"column:tool_result_compression_enabled"`
	// ToolResultMinifyDedup: lossless structural minify + dedup of tool results.
	ToolResultMinifyDedup          *bool  `gorm:"column:tool_result_minify_dedup"`
	ToolResultCompressionThreshold *int64 `gorm:"column:tool_result_compression_threshold"`
	// ToolResultAgeKeep: number of most-recent tool results kept in full before
	// older ones collapse to expandable stale references. NULL = default (6); 0 = disabled.
	ToolResultAgeKeep *int64 `gorm:"column:tool_result_age_keep"`
	// ToolResultExtractiveSearch: when true, large `search` tool results are
	// summarized via extractive selection regardless of the global method.
	// NULL/false = off.
	ToolResultExtractiveSearch *bool `gorm:"column:tool_result_extractive_search"`
	// QueuePaused stops this session's message queue being drained. Lives on the
	// session (not in the drainer) on purpose: drainers are ordinary clients and
	// there can be several — a host process in the background, a browser tab — so a
	// pause that lived in one of them would not be a pause at all. Enforced
	// server-side in ClaimNext, so an old or careless client cannot ignore it.
	// NULL/false = running, so sessions that predate the column read as unpaused
	// without a backfill.
	QueuePaused *bool `gorm:"column:queue_paused"`
	// ForkedFromSessionID / ForkedFromMessageID record where a forked session was
	// cloned from (see SessionRepo.ForkSession). Informational labels only, NOT
	// foreign keys: a fork owns a full copy of its data and never reads through to
	// the source, so deleting the source leaves these as harmless dangling labels.
	// Empty for sessions that were not forked. ForkedFromMessageID is empty when
	// the whole source history was cloned.
	ForkedFromSessionID string `gorm:"column:forked_from_session_id;size:256"`
	ForkedFromMessageID string `gorm:"column:forked_from_message_id;size:128"`
	// LastMessageAt is the newest llmd_chat_messages.created_at of the session
	// (NULL when it has none). Maintained by refreshSessionLastMessageAt on
	// message insert/delete so ListSessions needs no aggregate over messages.
	LastMessageAt *time.Time `gorm:"column:last_message_at"`
	CreatedAt           time.Time
	UpdatedAt           time.Time `gorm:"index:idx_sessions_updated,priority:3;index:idx_sessions_surface_updated,priority:4"`
}

func (SessionRow) TableName() string { return "llmd_sessions" }

// Chat message row kinds (persisted llmd_chat_messages.kind).
const (
	ChatMsgKindUser       = "user"
	ChatMsgKindSystem     = "system"
	ChatMsgKindAssistant  = "assistant"
	ChatMsgKindThinking   = "thinking"
	ChatMsgKindActivity   = "activity"
	ChatMsgKindTool       = "tool"
	ChatMsgKindToolResult = "tool_result"
	ChatMsgKindAttachment = "attachment"

	// ChatMsgKindCapturedAssistant and ChatMsgKindCapturedToolResult store provider-internal
	// tool loop messages (e.g. Claude Code / Cursor internal rounds) for diagnostic visibility.
	// These kinds are intentionally excluded from History() so they never reach the LLM context
	// on subsequent turns — only the diagnostic context view surfaces them.
	ChatMsgKindCapturedAssistant  = "captured_assistant"
	ChatMsgKindCapturedToolResult = "captured_tool_result"
)

// ChatMessageRow is one slice of a persisted conversation turn: content, thinking, tool calls, etc.
// Rows that belong to the same assistant llmv1.Message share TurnID and MessageID.
type ChatMessageRow struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	TenantID  string `gorm:"column:tenant_id;index:idx_chat_sess,priority:1;index:idx_chat_messages_recency,priority:1;index:idx_chat_messages_kind,priority:1;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;index:idx_chat_sess,priority:2;index:idx_chat_messages_recency,priority:2;index:idx_chat_messages_kind,priority:3;size:256;not null;default:''"`
	SessionID string `gorm:"index:idx_chat_sess,priority:3;index:idx_chat_messages_recency,priority:3;size:256;not null"`
	TurnID    string `gorm:"index:idx_chat_sess,priority:4;size:128;not null"`
	// TurnSeq is the canonical session-scoped, gap-free monotonic turn identity
	// minted by TurnRegister (llmd_session_turns.turn_seq). All rows of one
	// conversation turn — user, assistant parts, and tool results — share it, so a
	// tool result is attributable to the turn that issued it. Nullable: rows written
	// by callers without a known turn (e.g. bulk history replace) leave it NULL.
	// External/string turn ids live only in llmd_session_turns.external_turn_id;
	// TurnID here is a legacy per-row grouping label, not the turn identity.
	TurnSeq       *int64    `gorm:"column:turn_seq;index"`
	// idx_chat_messages_kind serves cross-session reads of one kind (open ask
	// cards across a tenant or scope) without scanning every message.
	Kind          string    `gorm:"index:idx_chat_messages_kind,priority:2;size:32;not null"`
	Role          string    `gorm:"size:32;not null"`
	MessageID     string    `gorm:"size:128"`
	Content       string    `gorm:"type:text"`
	ToolCallID    string    `gorm:"column:tool_call_id;size:128"`
	ToolCallsJSON string    `gorm:"column:tool_calls_json;type:text"`
	FinishReason  string    `gorm:"column:finish_reason;size:128"` // Stored on assistant content rows to indicate completion status (e.g. "cancelled")
	CreatedAt     time.Time `gorm:"index:idx_chat_messages_recency,priority:4"`
}

func (ChatMessageRow) TableName() string { return "llmd_chat_messages" }

// AfterCreate keeps llmd_sessions.last_message_at current for every message
// insert, whichever write path made it (deletes are handled by their callers).
func (m *ChatMessageRow) AfterCreate(tx *gorm.DB) error {
	return refreshSessionLastMessageAt(tx.Session(&gorm.Session{NewDB: true}),
		serverutil.Scope{TenantID: m.TenantID, ScopeID: m.ScopeID}, m.SessionID)
}

// TrackedDocumentVersionRow persists one immutable snapshot of a tracked document
// (a plan/design/idea body, or any context document) as attached to a session at a
// point in time. The client sends the full CURRENT content whenever a document is in
// scope; llmd hash-dedups against the latest version for that DocumentID and appends
// a new row only when the content actually changed. The model-facing representation
// (full body on first sighting vs a derived diff later) is assembled at send time
// from these snapshots and is NEVER stored here: snapshots are the source of truth,
// the presented form is derived. It doesn't matter who changed the document (user or
// agent tool edit) — llmd cares only that the content hash differs.
type TrackedDocumentVersionRow struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	TenantID  string `gorm:"column:tenant_id;index:idx_tracked_doc_lookup,priority:1;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;index:idx_tracked_doc_lookup,priority:2;size:256;not null;default:''"`
	SessionID string `gorm:"index:idx_tracked_doc_lookup,priority:3;size:256;not null"`
	// DocumentID is the document's stable identity across turns (plan/design/idea
	// docUuid; a review id for security reviews). Opaque to llmd — a join key only.
	DocumentID string `gorm:"index:idx_tracked_doc_lookup,priority:4;column:document_id;size:256;not null"`
	// Seq is the 0-based version index per (tenant, scope, session, document); each new
	// distinct snapshot increments it.
	Seq int `gorm:"not null;default:0"`
	// AnchorTurnID / AnchorMessageID record the turn + user message this version was
	// attached at, so send-time assembly can inject the version at the right position
	// and freeze it there.
	AnchorTurnID    string `gorm:"column:anchor_turn_id;size:128"`
	AnchorMessageID string `gorm:"column:anchor_message_id;size:128"`
	// Name is the document's display name at attach time (best-effort, for rendering).
	Name        string `gorm:"size:512"`
	ContentHash string `gorm:"column:content_hash;size:64;not null"`
	Content     string `gorm:"type:text"` // full snapshot, verbatim
	CreatedAt   time.Time
}

func (TrackedDocumentVersionRow) TableName() string { return "llmd_tracked_document_versions" }

// --- Provider storage ---------------------------------------------------
//
// llmd stores provider config as real typed columns, one table per provider
// kind — there is no serialized blob (no YAML, no JSON document) anywhere in
// this path. There are only a handful of provider kinds (7 LLM, 3
// embedding), so "one table per kind" is cheap and every field is a real,
// queryable, migratable column. A provider is a parent row (id + kind) plus
// exactly one matching row in the kind-specific child table below — class-
// table-inheritance, not a discriminated blob.
//
// Keyed by (tenant_id, scope_id, provider_id). A row with scope_id = '' is
// tenant-wide and visible from every scope in its tenant; a row in a specific
// scope with the same provider_id overrides it there (reads fall through
// scoped-first, tenant-wide second; writes and deletes are exact-scope).
// Tenants never see each other's providers.
//
// The only fields stored as a serialized column are the handful of small
// string-LIST fields on Claude Code config (add_dir, allowed_tools, ...):
// SQL has no native array type and a join table per list is not worth it for
// a locally-run single-process store, so those use GORM's built-in JSON
// serializer (a real column, genuinely typed as []string in Go — GORM
// marshals it, callers never see or handle JSON text).

// LlmProviderRow is the parent row for a registered LLM provider: the
// provider_id and which LlmProvider<Kind>Row table below holds its config.
type LlmProviderRow struct {
	TenantID   string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID    string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID string `gorm:"primaryKey;size:128;not null"`
	// Kind matches providerkind.Kind.String() (openai, ollama, anthropic,
	// cursor_cli, claude_code_v2, bedrock, gemini).
	Kind      string `gorm:"size:32;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (LlmProviderRow) TableName() string { return "llmd_llm_providers" }

// LlmProviderModelRow is one discovered model for a provider: a durable cache
// of the last successful catalog scan (providers.Composite.RefreshModels), so
// a restart or a provider-config edit does not silently empty the catalog.
//
// This is a CACHE, not a registry, and deliberately not the old
// llmd_registry_models: keyed by the exact (tenant_id, scope_id) of the
// provider row it was scanned for (no fall-through), no enabled/include_by_default
// policy columns, no allow/deny semantics. llmd does not decide which models a
// consumer may use; it only remembers which ones the provider reported. Rows
// are replaced wholesale per provider on each successful scan — never merged,
// so a model the provider stopped offering disappears instead of lingering.
//
// Per-model options (context window, effort/thinking defaults, pricing) will be
// added as columns here later; nothing about that needs a second table.
type LlmProviderModelRow struct {
	TenantID   string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID    string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID string `gorm:"primaryKey;size:128;not null"`
	// ModelKey is the provider-scoped model identifier a caller passes back to
	// llmd as the model to use: an upstream model id (gpt-4o,
	// claude-sonnet-4-5) or a synthesized variant key for providers with no
	// list-models API (sonnet, sonnet-low, sonnet-low-enabled — see
	// providers.scanClaudeCode). Unique per provider, hence the composite PK.
	ModelKey string `gorm:"primaryKey;column:model_key;size:256;not null"`
	// Label is the human-facing name for pickers ("Sonnet — Low (Thinking)").
	// Falls back to ModelKey when the provider offers nothing better.
	Label string `gorm:"column:label;size:512"`
	// DiscoveredAt is when the scan that produced this row completed. Every row
	// from one scan shares a timestamp, which is what makes it usable as the
	// catalog's as-of time after a restart.
	DiscoveredAt time.Time `gorm:"column:discovered_at;not null"`
}

func (LlmProviderModelRow) TableName() string { return "llmd_llm_provider_models" }

type LlmProviderAnthropicRow struct {
	TenantID        string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID         string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID      string `gorm:"primaryKey;size:128;not null"`
	APIKey          string `gorm:"column:api_key;type:text;not null"`
	BaseURL         string `gorm:"column:base_url;size:512"`
	DefaultModel    string `gorm:"column:default_model;size:256"`
	MaxOutputTokens int64  `gorm:"column:max_output_tokens"`
}

func (LlmProviderAnthropicRow) TableName() string { return "llmd_llm_provider_anthropic" }

type LlmProviderOpenAIRow struct {
	TenantID     string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID      string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID   string `gorm:"primaryKey;size:128;not null"`
	APIKey       string `gorm:"column:api_key;type:text;not null"`
	BaseURL      string `gorm:"column:base_url;size:512"`
	DefaultModel string `gorm:"column:default_model;size:256"`
	MaxTokens    int    `gorm:"column:max_tokens"`
}

func (LlmProviderOpenAIRow) TableName() string { return "llmd_llm_provider_openai" }

type LlmProviderOllamaRow struct {
	TenantID     string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID      string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID   string `gorm:"primaryKey;size:128;not null"`
	BaseURL      string `gorm:"column:base_url;size:512;not null"`
	DefaultModel string `gorm:"column:default_model;size:256"`
	MaxTokens    int    `gorm:"column:max_tokens"`
}

func (LlmProviderOllamaRow) TableName() string { return "llmd_llm_provider_ollama" }

type LlmProviderCursorCLIRow struct {
	TenantID        string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID         string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID      string `gorm:"primaryKey;size:128;not null"`
	Binary          string `gorm:"column:binary;size:512"`
	Cwd             string `gorm:"column:cwd;size:1024"`
	Workspace       string `gorm:"column:workspace;size:1024"`
	APIKey          string `gorm:"column:api_key;type:text"`
	DefaultModel    string `gorm:"column:default_model;size:256"`
	Mode            string `gorm:"column:mode;size:32"`
	Trust           *bool  `gorm:"column:trust"`
	ApproveMcps     *bool  `gorm:"column:approve_mcps"`
	Force           *bool  `gorm:"column:force"`
	SessionID       string `gorm:"column:session_id;size:256"`
	MaxOutputTokens int    `gorm:"column:max_output_tokens"`
}

func (LlmProviderCursorCLIRow) TableName() string { return "llmd_llm_provider_cursor_cli" }

type LlmProviderClaudeCodeRow struct {
	TenantID             string   `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID              string   `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID           string   `gorm:"primaryKey;size:128;not null"`
	Binary               string   `gorm:"column:binary;size:512"`
	Cwd                  string   `gorm:"column:cwd;size:1024"`
	Agent                string   `gorm:"column:agent;size:128"`
	DefaultModel         string   `gorm:"column:default_model;size:256"`
	SystemPrompt         string   `gorm:"column:system_prompt;type:text"`
	AppendSystemPrompt   string   `gorm:"column:append_system_prompt;type:text"`
	Effort               string   `gorm:"column:effort;size:32"`
	PermissionMode       string   `gorm:"column:permission_mode;size:64"`
	MaxBudgetUSD         *float64 `gorm:"column:max_budget_usd"`
	AddDir               []string `gorm:"column:add_dir;type:text;serializer:json"`
	AllowedTools         []string `gorm:"column:allowed_tools;type:text;serializer:json"`
	DisallowedTools      []string `gorm:"column:disallowed_tools;type:text;serializer:json"`
	McpConfigPath        string   `gorm:"column:mcp_config_path;size:1024"`
	TimeoutMs            int      `gorm:"column:timeout_ms"`
	MaxTurns             int      `gorm:"column:max_turns"`
	SettingSources       []string `gorm:"column:setting_sources;type:text;serializer:json"`
	ThinkingType         string   `gorm:"column:thinking_type;size:32"`
	ThinkingBudgetTokens *int     `gorm:"column:thinking_budget_tokens"`
	// HooksJSON is passed through verbatim into the CLI's --settings hooks key; its shape is the
	// CLI's own hook-config schema, not llmd's, so it is legitimately opaque here.
	HooksJSON           string `gorm:"column:hooks_json;type:text"`
	StrictMcpConfig     *bool  `gorm:"column:strict_mcp_config"`
	IncludeHookEvents   *bool  `gorm:"column:include_hook_events"`
	ConfigDir           string `gorm:"column:config_dir;size:1024"`
	UseMyClaudeConfig   *bool  `gorm:"column:use_my_claude_config"`
	DeferToolSchemas    *bool  `gorm:"column:defer_tool_schemas"`
	UseClaudeCodePreset *bool  `gorm:"column:use_claude_code_preset"`
	OAuthToken          string `gorm:"column:oauth_token;type:text"`
}

func (LlmProviderClaudeCodeRow) TableName() string { return "llmd_llm_provider_claude_code" }

type LlmProviderBedrockRow struct {
	TenantID        string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID         string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID      string `gorm:"primaryKey;size:128;not null"`
	Region          string `gorm:"column:region;size:64;not null"`
	Profile         string `gorm:"column:profile;size:128"`
	DefaultModel    string `gorm:"column:default_model;size:256"`
	MaxOutputTokens int64  `gorm:"column:max_output_tokens"`
	EndpointURL     string `gorm:"column:endpoint_url;size:512"`
	AssumeRoleARN   string `gorm:"column:assume_role_arn;size:2048"`
	RoleSessionName string `gorm:"column:role_session_name;size:128"`
	// size 1224: AWS STS maximum length of an ExternalId is 1224 characters.
	ExternalID string `gorm:"column:external_id;size:1224"`
}

func (LlmProviderBedrockRow) TableName() string { return "llmd_llm_provider_bedrock" }

type LlmProviderGeminiRow struct {
	TenantID        string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID         string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID      string `gorm:"primaryKey;size:128;not null"`
	APIKey          string `gorm:"column:api_key;type:text;not null"`
	BaseURL         string `gorm:"column:base_url;size:512"`
	DefaultModel    string `gorm:"column:default_model;size:256"`
	MaxOutputTokens int64  `gorm:"column:max_output_tokens"`
}

func (LlmProviderGeminiRow) TableName() string { return "llmd_llm_provider_gemini" }

// LlmProviderOpenRouterRow stores an OpenRouter provider. The default call options are kept in
// typed columns: Sort is the llmv1.OpenRouterCallOptions_Sort enum number (0 = unspecified),
// the lists are JSON arrays, and AllowFallbacks is NULL when unset.
type LlmProviderOpenRouterRow struct {
	TenantID           string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID            string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID         string `gorm:"primaryKey;size:128;not null"`
	APIKey             string `gorm:"column:api_key;type:text;not null"`
	BaseURL            string `gorm:"column:base_url;size:512"`
	DefaultModel       string `gorm:"column:default_model;size:256"`
	Sort               int32    `gorm:"column:sort;not null;default:0"`
	ProviderOrder      []string `gorm:"column:provider_order;type:text;serializer:json"`
	ProviderOnly       []string `gorm:"column:provider_only;type:text;serializer:json"`
	ProviderIgnore     []string `gorm:"column:provider_ignore;type:text;serializer:json"`
	AllowFallbacks     *bool    `gorm:"column:allow_fallbacks"`
	// AvoidUnhealthyEndpoints is NULL when unset (runtime default: enabled).
	AvoidUnhealthyEndpoints *bool `gorm:"column:avoid_unhealthy_endpoints"`
	// EndpointHealthTTLSeconds is 0 when unset (runtime default: 60).
	EndpointHealthTTLSeconds int32 `gorm:"column:endpoint_health_ttl_seconds;not null;default:0"`
}

func (LlmProviderOpenRouterRow) TableName() string { return "llmd_llm_provider_openrouter" }

// LlmProviderConfig is the resolved typed config for one LLM provider — exactly one of the
// kind-specific fields is set, matching Kind. Returned by LlmProviderRepo.Get; consumed by
// internal/llm/providers.BuildEntryFromProviderConfig (no parsing — these ARE the columns).
//
// Kind is the enum, not its id. It was a bare string, which made "no parsing" above a lie:
// every consumer had to call a lenient parser to get back the closed type it started with,
// and that parser was where alias spellings and silent zero values crept in. The id/enum
// conversion now happens exactly once, in LlmProviderRepo, at the column boundary.
type LlmProviderConfig struct {
	ProviderID string
	Kind       providerkind.Kind
	Anthropic  *LlmProviderAnthropicRow
	OpenAI     *LlmProviderOpenAIRow
	Ollama     *LlmProviderOllamaRow
	CursorCLI  *LlmProviderCursorCLIRow
	ClaudeCode *LlmProviderClaudeCodeRow
	Bedrock    *LlmProviderBedrockRow
	Gemini     *LlmProviderGeminiRow
	OpenRouter *LlmProviderOpenRouterRow
}

// EmbeddingProviderRow is the parent row for a registered embedding provider.
// Keyed by (tenant_id, scope_id, provider_id) like LlmProviderRow: reads fall
// back from the scoped row to the tenant-wide (scope_id = '') row.
type EmbeddingProviderRow struct {
	TenantID   string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID    string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID string `gorm:"primaryKey;size:128;not null"`
	// Kind is one of: ollama, openai_compat, bedrock.
	Kind      string `gorm:"size:32;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (EmbeddingProviderRow) TableName() string { return "llmd_embedding_providers" }

type EmbeddingProviderOllamaRow struct {
	TenantID   string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID    string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID string `gorm:"primaryKey;size:128;not null"`
	BaseURL    string `gorm:"column:base_url;size:512;not null"`
	EmbedModel string `gorm:"column:embed_model;size:256;not null"`
	Dimensions int    `gorm:"column:dimensions;not null"`
}

func (EmbeddingProviderOllamaRow) TableName() string { return "llmd_embedding_provider_ollama" }

type EmbeddingProviderOpenAICompatRow struct {
	TenantID   string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID    string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID string `gorm:"primaryKey;size:128;not null"`
	BaseURL    string `gorm:"column:base_url;size:512;not null"`
	APIKey     string `gorm:"column:api_key;type:text"`
	EmbedModel string `gorm:"column:embed_model;size:256;not null"`
	Dimensions int    `gorm:"column:dimensions;not null"`
}

func (EmbeddingProviderOpenAICompatRow) TableName() string {
	return "llmd_embedding_provider_openai_compat"
}

type EmbeddingProviderBedrockRow struct {
	TenantID    string `gorm:"primaryKey;column:tenant_id;size:128;not null"`
	ScopeID     string `gorm:"primaryKey;column:scope_id;size:128;not null;default:''"`
	ProviderID  string `gorm:"primaryKey;size:128;not null"`
	Region      string `gorm:"column:region;size:64;not null"`
	Profile     string `gorm:"column:profile;size:128"`
	EmbedModel  string `gorm:"column:embed_model;size:256;not null"`
	Dimensions  int    `gorm:"column:dimensions;not null"`
	EndpointURL string `gorm:"column:endpoint_url;size:512"`
}

func (EmbeddingProviderBedrockRow) TableName() string { return "llmd_embedding_provider_bedrock" }

// EmbeddingProviderConfig is the resolved typed config for one embedding provider — exactly one
// of the kind-specific fields is set, matching Kind.
type EmbeddingProviderConfig struct {
	ProviderID   string
	Kind         string
	Ollama       *EmbeddingProviderOllamaRow
	OpenAICompat *EmbeddingProviderOpenAICompatRow
	Bedrock      *EmbeddingProviderBedrockRow
}

// SchemaMigrationRow records one-time data migrations applied after GORM schema AutoMigrate.
type SchemaMigrationRow struct {
	Version   int `gorm:"primaryKey"`
	AppliedAt time.Time
}

func (SchemaMigrationRow) TableName() string { return "llmd_schema_migrations" }

// SessionTurnRow records one durable turn boundary for a session. turn_seq is a
// gap-free, monotonically increasing sequence issued by TurnRegister — the
// canonical turn identity, independent of any external/legacy turn id string.
type SessionTurnRow struct {
	TenantID  string `gorm:"column:tenant_id;primaryKey;index:idx_session_turns_lookup,priority:1;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;primaryKey;index:idx_session_turns_lookup,priority:2;size:256;not null;default:''"`
	SessionID string `gorm:"primaryKey;index:idx_session_turns_lookup,priority:3;size:256;not null"`
	TurnSeq   int64  `gorm:"primaryKey;autoIncrement:false;index:idx_session_turns_lookup,priority:4"`
	StartedAt time.Time
	// ExternalTurnID retains the caller-supplied turn id (if any) for correlation
	// with legacy per-turn tables that still key on it. Empty for MCP-driven turns.
	ExternalTurnID string `gorm:"column:external_turn_id;size:128"`
}

func (SessionTurnRow) TableName() string { return "llmd_session_turns" }

// TurnDecorationRow persists one typed, out-of-band decoration attached to a
// turn or to a single tool result. Decorations are passive, out-of-band
// pointers that must NEVER be folded into llmd_chat_messages content — they
// ride beside the turn, keyed by the same canonical identity the tool-result
// pipeline stamps everywhere: turn_seq (llmd_session_turns) for turn-level
// decorations and tool_call_id for result-level ones.
//
// llmd stores Kind and URI verbatim and never interprets either — all data
// (confidence, labels, counts, …) lives at whatever URI resolves to, owned by
// the emitting system. Any tool/transport can write through the toolsession
// seam.
type TurnDecorationRow struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	TenantID  string `gorm:"column:tenant_id;index:idx_turn_decorations_lookup,priority:1;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;index:idx_turn_decorations_lookup,priority:2;size:256;not null;default:''"`
	SessionID string `gorm:"index:idx_turn_decorations_lookup,priority:3;size:256;not null"`
	// TurnSeq is the canonical llmd_session_turns.turn_seq, stamped from the
	// tool-invocation envelope. Nullable for rows written before a turn is known.
	TurnSeq *int64 `gorm:"column:turn_seq;index"`
	// TurnID is the legacy/external turn label ("" for MCP-authored rows).
	TurnID string `gorm:"column:turn_id;size:128"`
	// Target scopes the decoration: "turn" (turn-level, ToolCallID empty) or
	// "tool_result" (attached to one tool result via ToolCallID).
	Target     string `gorm:"size:32;not null"`
	ToolCallID string `gorm:"column:tool_call_id;size:128;index"`
	// Kind discriminates the decoration type (e.g. "host_provenance",
	// "entity_reference") for interested readers. Opaque to llmd; the emitter
	// owns the vocabulary.
	Kind string `gorm:"size:64;not null"`
	// URI is an opaque pointer to the emitter-owned data this decoration
	// references — the ONLY payload column, by design. llmd never parses it.
	URI       string `gorm:"column:uri;type:text"`
	CreatedAt time.Time
}

func (TurnDecorationRow) TableName() string { return "llmd_turn_decorations" }

// TurnMetadataRow persists per-LLM-turn token accounting and compression stats.
// One row per turn: turn-scoped rather than row-scoped to avoid duplication across the
// multi-row fan-out in buildAssistantTurnRows. Turns without usage columns stay nullable.
type TurnMetadataRow struct {
	TenantID  string `gorm:"column:tenant_id;primaryKey;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;primaryKey;size:256;not null;default:''"`
	SessionID string `gorm:"primaryKey;size:256"`
	TurnID    string `gorm:"primaryKey;size:128"`
	// MessageID is the assistant llmv1.Message.Id for this turn — used to join
	// with llmd_chat_messages.message_id when returning per-turn token counts.
	MessageID string `gorm:"column:message_id;size:128;index"`
	// ProviderID / ModelID record the provider+model that ACTUALLY served this turn,
	// stamped at turn-finish. Required for honest cost pricing: a session can be
	// replayed across multiple providers/models (see llmd_provider_session_sync), so
	// the session's current default model is not a reliable per-turn attribution.
	// Nullable: legacy rows fall back to the session model (best-effort).
	ProviderID             string `gorm:"column:provider_id;size:128;not null;default:''"`
	ModelID                string `gorm:"column:model_id;size:256;not null;default:''"`
	ProviderInToks         *int64 `gorm:"column:provider_in_toks"`
	ProviderOutToks        *int64 `gorm:"column:provider_out_toks"`
	ProviderCacheReadToks  *int64 `gorm:"column:provider_cache_read_toks"`
	ProviderCacheWriteToks *int64 `gorm:"column:provider_cache_write_toks"`
	OriginalTokens         *int64 `gorm:"column:original_tokens"`
	CompressedTokens       *int64 `gorm:"column:compressed_tokens"`
	CompressionMeta        string `gorm:"column:compression_meta;type:text"` // JSON: method, sidecar keys
	// ModelDurationMs is the wall time of this one model call (one round of the
	// tool loop), from just before the provider call to its turn-finished frame.
	// Nullable: rows written before it existed have no time.
	ModelDurationMs *int64 `gorm:"column:model_duration_ms"`
	CreatedAt       time.Time
	UpdatedAt              time.Time
}

func (TurnMetadataRow) TableName() string { return "llmd_turn_metadata" }

// ToolResultSidecarRow persists the full original (pre-compression) content of one tool result.
// Keyed by tool_call_id so expand_tool_result can retrieve it after restart/session resume.
type ToolResultSidecarRow struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	TenantID   string `gorm:"column:tenant_id;index:idx_sidecar_lookup,priority:1;size:256;not null"`
	ScopeID    string `gorm:"column:scope_id;index:idx_sidecar_lookup,priority:2;size:256;not null;default:''"`
	SessionID  string `gorm:"index:idx_sidecar_lookup,priority:3;size:256;not null"`
	ToolCallID string `gorm:"index:idx_sidecar_lookup,priority:4;column:tool_call_id;size:128;not null"`
	ToolName   string `gorm:"size:128"`
	Original   string `gorm:"type:text"` // full uncompressed tool result
	// Compression diagnostics — populated when the result was actually compressed,
	// so the compressed wire form and savings can be inspected directly in the row.
	Compressed     bool   `gorm:"not null;default:false"`
	Wire           string `gorm:"type:text"` // compressed content sent to the provider
	Method         string `gorm:"size:64;not null;default:''"`
	OriginalTokens int64  `gorm:"not null;default:0"`
	WireTokens     int64  `gorm:"not null;default:0"`
	CreatedAt      time.Time
}

func (ToolResultSidecarRow) TableName() string { return "llmd_tool_result_sidecar" }

// SessionCacheRow is one entry in the generic session-scoped K/V cache.
// Keyed by (tenant, scope, session, namespace, cache_key); values are opaque bytes that
// llmd never interprets. Treated as ephemeral/derivable — safe to lose.
type SessionCacheRow struct {
	TenantID  string `gorm:"column:tenant_id;primaryKey;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;primaryKey;size:256;not null;default:''"`
	SessionID string `gorm:"primaryKey;size:256"`
	Namespace string `gorm:"primaryKey;size:128;not null"`
	CacheKey  string `gorm:"primaryKey;column:cache_key;size:512;not null"`
	Value     []byte `gorm:"type:blob"`
	UpdatedAt time.Time
}

func (SessionCacheRow) TableName() string { return "llmd_session_cache" }

// SessionEventRow persists one ConversationEvent for replay and subscription.
// Keyed by (tenant, scope, session, seq); seq increases monotonically per scoped session.
// Used by SubscribeSessionEvents to stream snapshots and deltas to browsers.
type SessionEventRow struct {
	TenantID  string `gorm:"column:tenant_id;primaryKey;size:256;not null"`
	ScopeID   string `gorm:"column:scope_id;primaryKey;size:256;not null;default:''"`
	SessionID string `gorm:"primaryKey;size:256;not null"`
	Seq       int64  `gorm:"primaryKey;not null"` // Monotonically increasing per session
	EventJSON string `gorm:"type:text;not null"`  // Serialized ConversationEvent
	CreatedAt time.Time
}

func (SessionEventRow) TableName() string { return "llmd_session_events" }

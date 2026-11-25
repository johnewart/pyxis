package store

import "time"

// AgentProfileRow persists one dynamic (workspace-scoped) agent profile.
//
// Static profiles are code-defined in internal/agentprofile and never touch
// this table; only profiles created through the CRUD RPCs live here. A
// profile's name is unique within its workspace so the UI can address it by
// name without ambiguity, while the id is the stable primary key.
type AgentProfileRow struct {
	ID          string `gorm:"primaryKey;size:256"`
	TenantID    string `gorm:"column:tenant_id;uniqueIndex:idx_agent_profiles_scope_name,priority:1;size:256;not null"`
	ScopeID     string `gorm:"column:scope_id;uniqueIndex:idx_agent_profiles_scope_name,priority:2;size:256;not null;default:''"`
	Name        string `gorm:"column:name;uniqueIndex:idx_agent_profiles_scope_name,priority:3;size:256;not null"`
	Description string `gorm:"column:description;type:text"`
	// SystemPromptSectionsJSON is a JSON array of protojson-encoded
	// llmv1.SystemPromptSection messages ("" when none) — see
	// MarshalSystemPromptSectionsJSON.
	SystemPromptSectionsJSON string `gorm:"column:system_prompt_sections_json;type:text"`
	LlmProviderID            string `gorm:"column:llm_provider_id;size:256"`
	DefaultModel             string `gorm:"column:default_model;size:256"`
	MemoryEnabled            bool   `gorm:"column:memory_enabled"`
	// ToolMappingJSON is a JSON array of {"tool_name","enabled"} objects ("" when
	// none) — see MarshalToolMappingJSON.
	ToolMappingJSON string `gorm:"column:tool_mapping_json;type:text"`
	// ToolPolicy is the persisted spelling of the profile's tool policy (see
	// agentprofile.PolicyName): "" = inherit server-wide, "profile_only" = the
	// tool mapping is an allowlist. Empty for rows written before the column
	// existed, which reads back as inherit — no backfill.
	ToolPolicy string `gorm:"column:tool_policy;size:32"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (AgentProfileRow) TableName() string { return "llmd_agent_profiles" }

package store

import "gorm.io/gorm"

// CoreModels are every persistence model llmd owns: sessions, chat history,
// provider configuration and turn bookkeeping.
//
// Session-keyed rows are partitioned by (tenant_id, scope_id), opaque keys the
// caller chooses; llmd keeps no registry of tenants or scopes.
var CoreModels = []any{
	&SessionRow{},
	&ChatMessageRow{},
	&LlmProviderRow{},
	&LlmProviderAnthropicRow{},
	&LlmProviderOpenAIRow{},
	&LlmProviderOllamaRow{},
	&LlmProviderCursorCLIRow{},
	&LlmProviderClaudeCodeRow{},
	&LlmProviderBedrockRow{},
	&LlmProviderGeminiRow{},
	&LlmProviderOpenRouterRow{},
	&LlmProviderModelRow{},
	&EmbeddingProviderRow{},
	&EmbeddingProviderOllamaRow{},
	&EmbeddingProviderOpenAICompatRow{},
	&EmbeddingProviderBedrockRow{},
	&ProviderSessionSyncRow{},
	&SchemaMigrationRow{},
	&TurnMetadataRow{},
	&ToolResultSidecarRow{},
	&SessionCacheRow{},
	&SessionEventRow{},
	&TrackedDocumentVersionRow{},
	&SessionTurnRow{},
	&TurnDecorationRow{},
	&SessionDraftRow{},
	&SessionQueuedMessageRow{},
	&AgentProfileRow{},
	&UsageRecordRow{},
	&ToolUsageRow{},
}

// StepFunc runs fn as one named, timed step (a host's startup tracing) and
// returns fn's error. nil runs fn directly.
type StepFunc func(name string, fn func() error) error

// Run runs fn through s, or directly when s is nil.
func (s StepFunc) Run(name string, fn func() error) error {
	if s == nil {
		return fn()
	}
	return s(name, fn)
}

// AutoMigrate creates or updates schema for llmd persistence tables and runs data migrations.
func AutoMigrate(db *gorm.DB) error {
	return AutoMigrateSteps(db, nil)
}

// AutoMigrateSteps is AutoMigrate with each part run as a step: the schema
// migration, then each data migration that has not run yet, by name. A host
// passes its startup tracer so a slow migration shows by name instead of
// inside one opaque "open database" step.
func AutoMigrateSteps(db *gorm.DB, step StepFunc) error {
	if err := step.Run("migrate schema", func() error { return db.AutoMigrate(CoreModels...) }); err != nil {
		return err
	}
	// Run any recorded-once data migrations (see runDataMigrations).
	return runDataMigrations(db, step)
}


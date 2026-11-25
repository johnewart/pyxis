/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 */

/**
 * A model row in an application-side catalog assembled from llmd's
 * ListLlmCatalogModels (per-provider, refreshed on demand — see
 * internal/llm/providers.Composite on llmd's side). NOT per-workspace: llmd
 * has a single provider namespace keyed by provider id alone.
 *
 * Results are STABLE across llmd restarts: each successful
 * RefreshLlmProviderModels persists the provider's models (model key + label +
 * discovery time), and llmd rehydrates that cache when it resolves a provider.
 * So an empty `models` for a configured provider means "never scanned
 * successfully", not "llmd was restarted" — check the provider's
 * `modelsRefreshedUnixMs` (0 = never) to tell the two apart.
 *
 * llmd has no global model registry of its own and does not enforce any
 * allow/deny list; `catalogAvailable` here is a policy decision made by the
 * application consuming this type, not something llmd computes.
 */
export type CatalogModel = {
  canonicalId: string;
  providerId: string;
  modelId: string;
  displayName: string;
  /**
   * Whether the consuming application offers this model. Purely its own
   * policy: llmd stores no enabled/include-by-default flags (the tables that
   * once did were removed as unenforced), so nothing llmd returns sets this.
   */
  catalogAvailable: boolean;
  contextWindow?: number;
  claude_base_model?: string;
  claude_effort?: string;
  claude_thinking_type?: string;
  claude_thinking_budget_tokens?: number;
  claude_max_budget_usd?: number;
  filename?: string;
};

/** A provider row in the global catalog. */
export type CatalogProvider = {
  providerId: string;
  displayName: string;
  providerType: string;
  /** Has credentials (secret_id, base_url, env, etc.). */
  configured: boolean;
  defaultModel?: string;
  models: CatalogModel[];
};

/** Full global catalog — single source of truth for registry-backed models. */
export type GlobalModelCatalog = {
  providers: CatalogProvider[];
  llmdConnected: boolean;
  llmdError?: string;
};

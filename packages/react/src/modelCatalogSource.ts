/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Transport-agnostic conversion from llmd's provider/catalog RPC results
 * (`LlmProviderSummary`/`CatalogModelSummary`, one shape shared by both of
 * @pyxis-llm/client's transports) into `ChatModelOption[]` for the
 * catalog picker components. Node/browser hooks (see useLlmdModelCatalog.js
 * variants) both funnel through this.
 */

import { buildConfiguredModelOptions, type ChatModelOption } from './chatModelOptions.js';

export type LlmProviderSummaryLike = {
  id: string;
  type: string;
  defaultModel: string;
  catalogModelCount: number;
  /**
   * Last catalog discovery time in unix ms; 0 means never scanned. Optional so
   * older callers still type-check. Use it to distinguish a provider that needs
   * a one-time `refreshProvider(id)` from one that genuinely has no models.
   */
  modelsRefreshedUnixMs?: number;
  userRegistered: boolean;
  stateful: boolean;
};

export type CatalogModelSummaryLike = {
  providerId: string;
  modelId: string;
  displayName?: string;
};

export function chatModelOptionsFromCatalog(
  providers: LlmProviderSummaryLike[],
  models: CatalogModelSummaryLike[]
): ChatModelOption[] {
  const modelsByProvider = new Map<string, { id: string; name: string }[]>();
  for (const m of models) {
    const list = modelsByProvider.get(m.providerId) ?? [];
    list.push({ id: m.modelId, name: (m.displayName ?? '').trim() || m.modelId });
    modelsByProvider.set(m.providerId, list);
  }

  const rows = providers.map((p) => ({
    id: p.id,
    name: p.id,
    defaultModel: p.defaultModel,
    models: modelsByProvider.get(p.id) ?? [],
  }));

  return buildConfiguredModelOptions(rows);
}

/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 */

/**
 * Stable canonical model id (`provider/model`), matching
 * store.CanonicalModelID on llmd's side. llmd has no separate model
 * registry service — this mirrors the id shape returned by
 * ListLlmCatalogModels (per-provider catalog, refreshed on demand and
 * persisted across restarts). `modelId` is the provider-scoped model key:
 * either an upstream id (gpt-4o) or a synthesized variant key for providers
 * with no list-models API (sonnet-low-enabled).
 */
export function toCanonicalModelId(providerId: string, modelId: string): string {
  const pid = providerId.trim();
  const mid = modelId.trim();
  if (!mid) return '';
  if (mid.includes('/')) return mid;
  if (!pid) return mid;
  return `${pid}/${mid}`;
}

/** Extract chat model id from a canonical id for legacy picker rows. */
export function modelIdFromCanonicalId(
  canonicalId: string,
  providerId: string
): string {
  const cid = canonicalId.trim();
  const pid = providerId.trim();
  const prefix = pid ? `${pid}/` : '';
  if (prefix && cid.startsWith(prefix)) {
    return cid.slice(prefix.length);
  }
  const slash = cid.indexOf('/');
  if (slash >= 0 && slash < cid.length - 1) {
    return cid.slice(slash + 1);
  }
  return cid;
}

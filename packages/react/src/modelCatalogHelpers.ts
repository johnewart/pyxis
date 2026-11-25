/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Generic helpers for resolving/validating a provider+model pair against a
 * catalog. Deliberately does NOT include layered resolution (session
 * override > agent default > workspace default > catalog fallback, etc.) —
 * that's product-specific policy a host builds on top of these primitives.
 */

import type { ChatModelOption } from './chatModelOptions.js';

export type StoredModelPair = { providerId: string; modelId: string };

export function modelOptionExists(
  options: ChatModelOption[],
  providerId: string,
  modelId: string
): boolean {
  return options.some(
    (o) => o.providerId === providerId && o.modelId === modelId
  );
}

function normalizeModelToken(value: string): string {
  return value.trim().toLowerCase();
}

/**
 * Map a persisted provider+model to the current catalog row when possible
 * (case drift, or a path-shaped id like "org/model" vs. just "model").
 * Returns null when no catalog row matches; callers keep the persisted ids as-is.
 */
export function resolveCanonicalModelOption(
  options: ChatModelOption[],
  stored: StoredModelPair
): StoredModelPair | null {
  if (options.length === 0) return null;
  const pid = stored.providerId.trim();
  const mid = stored.modelId.trim();
  if (!pid || !mid) return null;

  const exact = options.find(
    (o) => o.providerId === pid && o.modelId === mid
  );
  if (exact) {
    return { providerId: exact.providerId, modelId: exact.modelId };
  }

  const pidLower = normalizeModelToken(pid);
  const midNorm = normalizeModelToken(mid);
  const caseInsensitive = options.find(
    (o) =>
      normalizeModelToken(o.providerId) === pidLower &&
      normalizeModelToken(o.modelId) === midNorm
  );
  if (caseInsensitive) {
    return {
      providerId: caseInsensitive.providerId,
      modelId: caseInsensitive.modelId,
    };
  }

  const midBase = mid.split(/[/\\]/).pop()?.trim() ?? mid;
  const baseNorm = normalizeModelToken(midBase);
  const baseMatch = options.find((o) => {
    if (normalizeModelToken(o.providerId) !== pidLower) return false;
    return (
      normalizeModelToken(o.modelId) === baseNorm ||
      normalizeModelToken(o.modelName) === baseNorm
    );
  });
  if (baseMatch) {
    return { providerId: baseMatch.providerId, modelId: baseMatch.modelId };
  }

  return null;
}

export function formatEffectiveModelLabel(
  providerId: string | null,
  modelId: string | null,
  allModelOptions: ChatModelOption[]
): string {
  if (allModelOptions.length === 0) {
    if (providerId && modelId) return `${providerId}: ${modelId}`;
    return 'No model available';
  }
  if (!providerId || !modelId) return 'No model available';
  const opt = allModelOptions.find(
    (o) => o.providerId === providerId && o.modelId === modelId
  );
  return opt ? `${opt.providerName}: ${opt.modelName}` : `${providerId}: ${modelId}`;
}

/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Binds CatalogModelPicker to a session's model. Lives beside
 * useLlmdModelCatalog (browser/node entries, not the main one) because it
 * takes an @pyxis-llm/client LlmdClient.
 *
 * Why this exists: a chat turn only carries a model NAME; the provider is
 * session state (UpdateSessionLlm). A picker that only keeps its selection in
 * host state looks right but leaves the session's provider unset, and every
 * send then fails with `unknown or unavailable llm provider ""`.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import type { LlmdClient } from '@pyxis-llm/client';

export type LlmdSessionModelSelection = { providerId: string; modelId: string };

export type LlmdSessionModel = {
  /** The session's provider + model; null until loaded or when none is set. */
  selected: LlmdSessionModelSelection | null;
  /** True while the session's current selection is being read. */
  loading: boolean;
  /** Last load/select failure. */
  error: Error | null;
  /**
   * Persists a new provider + model on the session. Optimistic: `selected`
   * changes at once and reverts (with `error` set) if llmd rejects it. Pass
   * straight to CatalogModelPicker's `onSelect`.
   */
  select: (providerId: string, modelId: string) => Promise<void>;
};

type SessionModelClient = Pick<LlmdClient, 'getSessionModel' | 'setSessionModel'>;

/**
 * Reads and sets the provider/model of `client`'s session. Wire to the picker:
 *
 *   const model = useLlmdSessionModel(client)
 *   <CatalogModelPicker
 *     selectedProviderId={model.selected?.providerId ?? null}
 *     selectedModelId={model.selected?.modelId ?? null}
 *     onSelect={model.select} … />
 *
 * `client` may be null/undefined while a host is still connecting.
 */
export function useLlmdSessionModel(
  client: SessionModelClient | null | undefined
): LlmdSessionModel {
  const [selected, setSelected] = useState<LlmdSessionModelSelection | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  // Bumped per client and per select, so a slow load cannot overwrite a newer
  // selection, and a stale select cannot revert one made after it.
  const generationRef = useRef(0);
  const selectedRef = useRef(selected);
  selectedRef.current = selected;

  useEffect(() => {
    const generation = ++generationRef.current;
    setSelected(null);
    setError(null);
    if (!client) return;
    setLoading(true);
    void (async () => {
      try {
        const cur = await client.getSessionModel();
        if (generationRef.current !== generation) return;
        setSelected(cur.providerId && cur.modelId ? cur : null);
      } catch (err) {
        if (generationRef.current !== generation) return;
        setError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        if (generationRef.current === generation) setLoading(false);
      }
    })();
  }, [client]);

  const select = useCallback(
    async (providerId: string, modelId: string) => {
      if (!client) return;
      const generation = ++generationRef.current;
      const previous = selectedRef.current;
      setSelected({ providerId, modelId });
      setError(null);
      setLoading(false);
      try {
        await client.setSessionModel(providerId, modelId);
      } catch (err) {
        if (generationRef.current !== generation) return;
        setSelected(previous);
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    },
    [client]
  );

  return { selected, loading, error, select };
}

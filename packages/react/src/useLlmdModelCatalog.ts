/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Node-capable model-catalog hook (see ./node.js). Fetches llmd's
 * registered providers + catalog models via @pyxis-llm/client and converts them
 * to ChatModelOption[] for CatalogModelPicker. Lives here (not the main
 * entry) for the same reason useLlmdChatDriver does: @pyxis-llm/client pulls in
 * @grpc/grpc-js.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import type { LlmdClient } from '@pyxis-llm/client';
import { chatModelOptionsFromCatalog } from './modelCatalogSource.js';
import type { ChatModelOption } from './chatModelOptions.js';

export type LlmdModelCatalog = {
  options: ChatModelOption[];
  loading: boolean;
  error: Error | null;
  refresh: () => void;
  /**
   * Re-scans a single provider's model catalog (RefreshLlmProviderModels) and
   * refetches options. Scoped to one provider so a host can wire this to a
   * "reload models" affordance on a per-provider nav item without paying for
   * the auto-refresh-everything cost `refresh()`/the initial load avoids.
   */
  refreshProvider: (providerId: string) => Promise<void>;
};

/**
 * Loads the model catalog (providers + their catalog models) for `client`'s
 * workspace and exposes it as `ChatModelOption[]` ready for
 * `CatalogModelPicker`'s `options` prop. `client` may be `null`/`undefined`
 * while a host is still setting up its connection.
 */
export function useLlmdModelCatalog(client: LlmdClient | null | undefined): LlmdModelCatalog {
  const [options, setOptions] = useState<ChatModelOption[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const generationRef = useRef(0);

  const refresh = useCallback(() => {
    if (!client) return;
    const generation = ++generationRef.current;
    setLoading(true);
    setError(null);
    void (async () => {
      try {
        const providers = await client.listProviders();
        // ListLlmCatalogModels never scans (see providers.Entry.CatalogLen), but
        // llmd now PERSISTS each successful scan and rehydrates it, so a provider
        // scanned once stays populated across llmd restarts — this load is both
        // cheap and stable. Still deliberately NOT auto-refreshing: a scan costs
        // a network round-trip / CLI spawn per provider and made initial load
        // very slow. Wire `refreshProvider(id)` to an explicit "refresh models"
        // affordance for when a provider's models have actually changed.
        //
        // A provider with no models has genuinely never been scanned
        // successfully (check its modelsRefreshedUnixMs === 0), and falls back to
        // a single "provider id as model" row below so it stays selectable.
        if (generationRef.current !== generation) return;
        const models = await client.listCatalogModels();
        if (generationRef.current !== generation) return;
        setOptions(chatModelOptionsFromCatalog(providers, models));
      } catch (err) {
        if (generationRef.current !== generation) return;
        setError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        if (generationRef.current === generation) setLoading(false);
      }
    })();
  }, [client]);

  useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- refresh already depends on client
  }, [client]);

  const refreshProvider = useCallback(
    async (providerId: string) => {
      if (!client) return;
      const generation = generationRef.current;
      try {
        await client.refreshProviderModels(providerId);
        if (generationRef.current !== generation) return;
        const [providers, models] = await Promise.all([
          client.listProviders(),
          client.listCatalogModels(),
        ]);
        if (generationRef.current !== generation) return;
        setOptions(chatModelOptionsFromCatalog(providers, models));
      } catch (err) {
        if (generationRef.current !== generation) return;
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    },
    [client]
  );

  return { options, loading, error, refresh, refreshProvider };
}

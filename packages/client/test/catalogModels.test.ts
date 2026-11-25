/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit tests for the model-catalog surface of LlmdClient — no llmd binary, no
 * network: a router transport stands in for LlmService.
 *
 * These pin the SDK-visible contract that llmd's durable per-provider model
 * cache exists to provide (internal/store.LlmProviderModelRow): a provider
 * scanned once keeps reporting its models, so `listCatalogModels()` is stable
 * and callers must NOT need a `refreshProviderModels()` on every startup. The
 * Go side proves the persistence itself
 * (internal/server/llm_provider_models_persist_test.go); this file proves the
 * mapping that carries model key + label + refresh time out to consumers.
 */

import { describe, expect, it } from 'vitest';
import { createRouterTransport } from '@connectrpc/connect';
import { LlmdClient } from '../src/llm.js';
import { LlmService, LlmProviderType } from '../src/gen/llmv1/llm_pb.js';

type CatalogRow = { providerId: string; modelId: string; displayName?: string };
type ProviderRow = {
  id: string;
  type: LlmProviderType;
  defaultModel: string;
  catalogModelCount: number;
  modelsRefreshedUnixMs: bigint;
  userRegistered: boolean;
  stateful: boolean;
};

/**
 * A client wired to a stub LlmService returning the given catalog. `refreshes`
 * records every RefreshLlmProviderModels call so a test can assert the SDK did
 * not scan behind the caller's back.
 */
function clientWith(opts: {
  models?: CatalogRow[];
  providers?: ProviderRow[];
  refreshes?: string[];
}): LlmdClient {
  const models = opts.models ?? [];
  const providers = opts.providers ?? [];
  const transport = createRouterTransport(({ service }) => {
    service(LlmService, {
      listLlmCatalogModels: (req) => {
        const filter = req.providerId?.trim();
        return {
          models: filter ? models.filter((m) => m.providerId === filter) : models,
        };
      },
      listLlmProviders: () => ({ providers }),
      refreshLlmProviderModels: (req) => {
        opts.refreshes?.push(req.providerId);
        return { modelsDiscovered: models.filter((m) => m.providerId === req.providerId).length };
      },
    });
  });
  return new LlmdClient({ transport, tenantId: 'tenant-test' });
}

describe('listCatalogModels', () => {
  it('carries the model key and label through for each provider', async () => {
    const client = clientWith({
      models: [
        { providerId: 'claude', modelId: 'sonnet', displayName: 'Sonnet' },
        {
          providerId: 'claude',
          modelId: 'sonnet-low-enabled',
          displayName: 'Sonnet — Low (Thinking)',
        },
      ],
    });

    const got = await client.listCatalogModels();

    expect(got).toEqual([
      { providerId: 'claude', modelId: 'sonnet', displayName: 'Sonnet' },
      {
        providerId: 'claude',
        modelId: 'sonnet-low-enabled',
        displayName: 'Sonnet — Low (Thinking)',
      },
    ]);
  });

  // The catalog is persisted server-side, so listing must never trigger a scan:
  // scanning costs a network round-trip or CLI spawn per provider.
  it('does not refresh providers as a side effect', async () => {
    const refreshes: string[] = [];
    const client = clientWith({
      models: [{ providerId: 'claude', modelId: 'sonnet', displayName: 'Sonnet' }],
      refreshes,
    });

    await client.listCatalogModels();

    expect(refreshes).toEqual([]);
  });

  it('scopes results to one provider when asked', async () => {
    const client = clientWith({
      models: [
        { providerId: 'claude', modelId: 'sonnet', displayName: 'Sonnet' },
        { providerId: 'openai', modelId: 'gpt-4o', displayName: 'GPT-4o' },
      ],
    });

    const got = await client.listCatalogModels('claude');

    expect(got.map((m) => m.modelId)).toEqual(['sonnet']);
  });

  // A model whose label the provider never supplied still has to be selectable.
  it('leaves displayName undefined rather than inventing one', async () => {
    const client = clientWith({
      models: [{ providerId: 'ollama', modelId: 'llama3' }],
    });

    const got = await client.listCatalogModels();

    expect(got).toHaveLength(1);
    expect(got[0]!.modelId).toBe('llama3');
    expect(got[0]!.displayName ?? '').toBe('');
  });

  it('returns an empty list for a provider that has never been scanned', async () => {
    const client = clientWith({ models: [] });

    await expect(client.listCatalogModels()).resolves.toEqual([]);
  });
});

describe('listProviders', () => {
  // modelsRefreshedUnixMs is how a consumer tells "never scanned" (0) from
  // "scanned, found nothing", so it must survive the bigint -> number mapping.
  it('reports the persisted catalog count and refresh time', async () => {
    const refreshedAt = Date.UTC(2026, 0, 2, 3, 4, 5);
    const client = clientWith({
      providers: [
        {
          id: 'claude',
          type: LlmProviderType.LLM_PROVIDER_TYPE_ANTHROPIC,
          defaultModel: 'claude-x',
          catalogModelCount: 2,
          modelsRefreshedUnixMs: BigInt(refreshedAt),
          userRegistered: true,
          stateful: false,
        },
      ],
    });

    const got = await client.listProviders();

    expect(got).toHaveLength(1);
    expect(got[0]!.id).toBe('claude');
    expect(got[0]!.catalogModelCount).toBe(2);
    expect(Number(got[0]!.modelsRefreshedUnixMs)).toBe(refreshedAt);
  });

  it('reports zero refresh time for a never-scanned provider', async () => {
    const client = clientWith({
      providers: [
        {
          id: 'fresh',
          type: LlmProviderType.LLM_PROVIDER_TYPE_OLLAMA,
          defaultModel: '',
          catalogModelCount: 0,
          modelsRefreshedUnixMs: 0n,
          userRegistered: true,
          stateful: false,
        },
      ],
    });

    const got = await client.listProviders();

    expect(got[0]!.catalogModelCount).toBe(0);
    expect(Number(got[0]!.modelsRefreshedUnixMs)).toBe(0);
  });
});

describe('refreshProviderModels', () => {
  it('scans only the named provider and returns the count discovered', async () => {
    const refreshes: string[] = [];
    const client = clientWith({
      models: [
        { providerId: 'claude', modelId: 'sonnet', displayName: 'Sonnet' },
        { providerId: 'claude', modelId: 'opus', displayName: 'Opus' },
        { providerId: 'openai', modelId: 'gpt-4o', displayName: 'GPT-4o' },
      ],
      refreshes,
    });

    await expect(client.refreshProviderModels('claude')).resolves.toBe(2);
    expect(refreshes).toEqual(['claude']);
  });
});

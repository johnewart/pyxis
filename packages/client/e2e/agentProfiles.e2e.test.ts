/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * End-to-end coverage for dynamic agent profiles against a REAL llmd
 * (spawned by the shared harness, real SQLite, real gRPC).
 *
 * Uses startE2eEnv (not a bare startLlmd like sessionTagsAndSurface) because a
 * profile that pins `llmProviderId` must name a provider the server knows —
 * the harness registers the local fake 'fake-openai' provider, so the Go
 * side's validateAgentLlmBinding accepts it without any network access.
 *
 * The harness THROWS in beforeAll when llmd-e2e-bin is absent; there is no
 * skip path, so a green run is real behavioural proof.
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { randomUUID } from 'node:crypto';
import {
  createLlmdAgentProfile,
  createLlmdSessionFromAgentProfile,
  deleteLlmdAgentProfile,
  getLlmdAgentProfile,
  getLlmdSessionMeta,
  listLlmdAgentProfiles,
  updateLlmdAgentProfile,
  type LlmdAgentProfile,
  type LlmdAgentProfileSpec,
} from '@pyxis-llm/client';

import { startE2eEnv, type E2eEnv } from './harness.js';

let env: E2eEnv;

beforeAll(async () => {
  env = await startE2eEnv();
}, 30_000);

afterAll(async () => {
  await env?.teardown();
});

const FAKE_MODEL = 'fake-model';

/** Call target shared by every wrapper: the harness's raw client + tenant/scope. */
function scope() {
  return { client: env.rawClient, tenantId: env.tenantId, scopeId: env.scopeId };
}

function uniqueName(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

async function createProfile(
  overrides: Partial<LlmdAgentProfileSpec> = {}
): Promise<LlmdAgentProfile> {
  return createLlmdAgentProfile({
    ...scope(),
    name: uniqueName('e2e-profile'),
    description: 'created by agentProfiles.e2e',
    llmProviderId: env.providerId,
    defaultModel: FAKE_MODEL,
    systemPromptSections: [
      { id: 'persona', content: 'You are an e2e fixture.', tags: ['e2e'] },
      { id: 'rules', content: 'Answer tersely.', tags: [] },
    ],
    ...overrides,
  });
}

describe('agent profiles: dynamic CRUD over the wire', () => {
  it('creates a dynamic profile and echoes its fields back', async () => {
    const name = uniqueName('e2e-create');
    const profile = await createProfile({ name, description: 'first' });

    expect(profile.id).not.toBe('');
    expect(profile.isStatic).toBe(false);
    expect(profile.createdAt).not.toBe('');
    expect(profile.updatedAt).not.toBe('');
    expect(profile.name).toBe(name);
    expect(profile.description).toBe('first');
    expect(profile.llmProviderId).toBe(env.providerId);
    expect(profile.defaultModel).toBe(FAKE_MODEL);
    expect(profile.memoryEnabled).toBe(false);
    expect(profile.toolMapping).toEqual([]);
    expect(profile.systemPromptSections).toEqual([
      { id: 'persona', content: 'You are an e2e fixture.', tags: ['e2e'] },
      { id: 'rules', content: 'Answer tersely.', tags: [] },
    ]);
  });

  it('lists the created profile and gets field-equal data back', async () => {
    const created = await createProfile();

    const listed = await listLlmdAgentProfiles(scope());
    const fromList = listed.find((p) => p.id === created.id);
    expect(fromList).toBeDefined();
    expect(fromList).toEqual(created);

    const fetched = await getLlmdAgentProfile({ ...scope(), profileId: created.id });
    expect(fetched).toEqual(created);
  });

  it('updates a profile wholesale and a subsequent get reflects it', async () => {
    const created = await createProfile();
    const newName = uniqueName('e2e-updated');

    const updated = await updateLlmdAgentProfile({
      ...scope(),
      id: created.id,
      name: newName,
      description: 'after update',
      // Update is wholesale: re-send provider/model or they are cleared.
      llmProviderId: env.providerId,
      defaultModel: FAKE_MODEL,
      systemPromptSections: [{ id: 'persona', content: 'Updated persona.', tags: ['v2'] }],
      memoryEnabled: true,
    });
    expect(updated.id).toBe(created.id);
    expect(updated.name).toBe(newName);
    expect(updated.description).toBe('after update');
    expect(updated.memoryEnabled).toBe(true);
    expect(updated.isStatic).toBe(false);

    const fetched = await getLlmdAgentProfile({ ...scope(), profileId: created.id });
    expect(fetched.name).toBe(newName);
    expect(fetched.description).toBe('after update');
    expect(fetched.llmProviderId).toBe(env.providerId);
    expect(fetched.defaultModel).toBe(FAKE_MODEL);
    expect(fetched.memoryEnabled).toBe(true);
    expect(fetched.systemPromptSections).toEqual([
      { id: 'persona', content: 'Updated persona.', tags: ['v2'] },
    ]);
    expect(fetched.createdAt).toBe(created.createdAt);
  });

  it('creates a session from a profile; session meta reflects provider/model and overrides', async () => {
    const profile = await createProfile();

    const sessionId = `e2e-sess-${randomUUID()}`;
    const result = await createLlmdSessionFromAgentProfile({
      ...scope(),
      agentProfileId: profile.id,
      sessionId,
    });
    expect(result).toEqual({ sessionId, agentProfileId: profile.id });

    const meta = await getLlmdSessionMeta({ ...scope(), sessionId });
    expect(meta.sessionId).toBe(sessionId);
    expect(meta.providerId).toBe(env.providerId);
    expect(meta.defaultModel).toBe(FAKE_MODEL);

    const overrideSessionId = `e2e-sess-${randomUUID()}`;
    const overrideModel = 'fake-model-override';
    await createLlmdSessionFromAgentProfile({
      ...scope(),
      agentProfileId: profile.id,
      sessionId: overrideSessionId,
      defaultModelOverride: overrideModel,
    });
    const overrideMeta = await getLlmdSessionMeta({ ...scope(), sessionId: overrideSessionId });
    expect(overrideMeta.providerId).toBe(env.providerId);
    expect(overrideMeta.defaultModel).toBe(overrideModel);
  });

  it('deletes a profile; get rejects afterwards but sessions created from it survive', async () => {
    const profile = await createProfile();
    const sessionId = `e2e-sess-${randomUUID()}`;
    await createLlmdSessionFromAgentProfile({
      ...scope(),
      agentProfileId: profile.id,
      sessionId,
    });

    await expect(
      deleteLlmdAgentProfile({ ...scope(), profileId: profile.id })
    ).resolves.toBeUndefined();

    await expect(
      getLlmdAgentProfile({ ...scope(), profileId: profile.id })
    ).rejects.toThrow(/not.?found/i);

    const listed = await listLlmdAgentProfiles(scope());
    expect(listed.find((p) => p.id === profile.id)).toBeUndefined();

    // No FK/join: the session is an ordinary session and keeps its config.
    const meta = await getLlmdSessionMeta({ ...scope(), sessionId });
    expect(meta.sessionId).toBe(sessionId);
    expect(meta.providerId).toBe(env.providerId);
    expect(meta.defaultModel).toBe(FAKE_MODEL);
  });

  it('rejects deleting an unknown profile id', async () => {
    await expect(
      deleteLlmdAgentProfile({ ...scope(), profileId: `missing-${randomUUID()}` })
    ).rejects.toThrow();
  });
});

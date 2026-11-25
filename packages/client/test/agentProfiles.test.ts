/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit tests for the agent-profile wrappers — no llmd binary, no network: a
 * router transport stands in for LlmService so the outgoing request shape can
 * be asserted directly.
 */

import { describe, expect, it } from 'vitest';
import { create } from '@bufbuild/protobuf';
import { createRouterTransport } from '@connectrpc/connect';
import {
  AgentProfileOverlayField,
  AgentProfileSchema,
  LlmService,
  type SetAgentProfileOverlayRequest,
  ToolPolicy,
  type CreateAgentProfileRequest,
  type CreateSessionFromAgentProfileRequest,
  type UpdateAgentProfileRequest,
} from '../src/gen/llmv1/llm_pb.js';
import {
  agentProfileFromProto,
  createLlmdAgentProfile,
  createLlmdSessionFromAgentProfile,
  listLlmdTools,
  setLlmdAgentProfileOverlay,
  updateLlmdAgentProfile,
} from '../src/agentProfiles.js';

describe('listLlmdTools', () => {
  // Regression: profile editors listed tools via listSessionTools, which needs
  // a session; a settings page has none, so the tool list was always empty.
  it('lists tenant tools through ListTools with the scope headers, sorted', async () => {
    let tenant: string | null = null;
    const transport = createRouterTransport(({ service }) => {
      service(LlmService, {
        listTools(_req, ctx) {
          tenant = ctx.requestHeader.get('llmd-tenant-id');
          return {
            tools: [
              { name: 'web_search', description: 'Search the web' },
              { name: ' ', description: 'blank names are dropped' },
              { name: 'read_code', description: '' },
            ],
          };
        },
      });
    });
    const tools = await listLlmdTools({ transport, tenantId: 'local', scopeId: 'default' });
    expect(tenant).toBe('local');
    expect(tools).toEqual([
      { name: 'read_code', description: '' },
      { name: 'web_search', description: 'Search the web' },
    ]);
  });
});

describe('setLlmdAgentProfileOverlay', () => {
  it('sends only the optionals that are set, and an explicit false', async () => {
    let seen: SetAgentProfileOverlayRequest | undefined;
    const transport = createRouterTransport(({ service }) => {
      service(LlmService, {
        setAgentProfileOverlay(req) {
          seen = req;
          return {
            overlay: { ...req.overlay, updatedAt: '2026-11-23T00:00:00Z' },
            profile: { id: 'plugin-editor', name: 'Editor', isStatic: true, customized: [AgentProfileOverlayField.TOOLS] },
          };
        },
      });
    });
    const res = await setLlmdAgentProfileOverlay({
      transport,
      tenantId: 'ws',
      overlay: {
        baseId: ' plugin-editor ',
        memoryEnabled: false,
        toolOverrides: [{ toolName: 'review', enabled: false }],
        sectionReplacements: [],
        extraSections: [{ id: 'mine', content: 'my rule' }],
      },
    });
    expect(seen?.overlay?.baseId).toBe('plugin-editor');
    expect(seen?.overlay?.memoryEnabled).toBe(false);
    expect(seen?.overlay?.defaultModel).toBeUndefined();
    expect(seen?.overlay?.llmProviderId).toBeUndefined();
    expect(seen?.overlay?.toolOverrides).toEqual([expect.objectContaining({ toolName: 'review', enabled: false })]);
    expect(seen?.overlay?.extraSections?.[0]?.id).toBe('mine');
    expect(res.overlay.updatedAt).toBe('2026-11-23T00:00:00Z');
    expect(res.profile.customized).toEqual([AgentProfileOverlayField.TOOLS]);
  });
});
import type { ClientTool } from '../src/llm.js';

describe('agentProfileFromProto', () => {
  it('maps every field, leaving unset optionals undefined', () => {
    const p = create(AgentProfileSchema, {
      id: 'static-ask',
      name: 'Ask',
      description: 'read-only helper',
      systemPromptSections: [{ id: 'intro', content: 'Be brief.', tags: ['core'] }],
      memoryEnabled: false,
      toolMapping: [
        { toolName: 'write_code', enabled: false },
        { toolName: 'search', enabled: true },
      ],
      createdAt: '',
      updatedAt: '',
      isStatic: true,
    });
    const out = agentProfileFromProto(p);
    expect(out).toEqual({
      id: 'static-ask',
      name: 'Ask',
      description: 'read-only helper',
      systemPromptSections: [{ id: 'intro', content: 'Be brief.', tags: ['core'] }],
      llmProviderId: undefined,
      defaultModel: undefined,
      memoryEnabled: false,
      toolMapping: [
        { toolName: 'write_code', enabled: false },
        { toolName: 'search', enabled: true },
      ],
      toolPolicy: ToolPolicy.UNSPECIFIED,
      createdAt: '',
      updatedAt: '',
      isStatic: true,
      customized: [],
    });
    // A disabled mapping must survive as an explicit false, never be dropped.
    expect(out.toolMapping[0].enabled).toBe(false);
  });

  it('carries the fields an overlay customized', () => {
    const p = create(AgentProfileSchema, {
      id: 'plugin-editor',
      name: 'Editor',
      isStatic: true,
      customized: [AgentProfileOverlayField.DEFAULT_MODEL, AgentProfileOverlayField.TOOLS],
    });
    expect(agentProfileFromProto(p).customized).toEqual([
      AgentProfileOverlayField.DEFAULT_MODEL,
      AgentProfileOverlayField.TOOLS,
    ]);
  });

  it('carries set optionals and dynamic-profile timestamps through', () => {
    const p = create(AgentProfileSchema, {
      id: 'dyn-1',
      name: 'Builder',
      description: '',
      llmProviderId: 'claude',
      defaultModel: 'sonnet',
      memoryEnabled: true,
      createdAt: '2026-09-28T00:00:00Z',
      updatedAt: '2026-09-28T01:00:00Z',
      isStatic: false,
    });
    const out = agentProfileFromProto(p);
    expect(out.llmProviderId).toBe('claude');
    expect(out.defaultModel).toBe('sonnet');
    expect(out.memoryEnabled).toBe(true);
    expect(out.isStatic).toBe(false);
    expect(out.createdAt).toBe('2026-09-28T00:00:00Z');
    expect(out.updatedAt).toBe('2026-09-28T01:00:00Z');
    expect(out.systemPromptSections).toEqual([]);
    expect(out.toolMapping).toEqual([]);
  });
});

/**
 * Stub LlmService that records the Create/UpdateAgentProfile requests it
 * receives and echoes a minimal profile back so the wrappers do not throw.
 */
function transportCapturingProfileWrites(seen: {
  create: CreateAgentProfileRequest[];
  update: UpdateAgentProfileRequest[];
}) {
  return createRouterTransport(({ service }) => {
    service(LlmService, {
      createAgentProfile: (req) => {
        seen.create.push(req);
        return {
          profile: create(AgentProfileSchema, {
            id: req.id ?? 'generated-id',
            name: req.name,
            toolMapping: req.toolMapping,
            toolPolicy: req.toolPolicy,
          }),
        };
      },
      updateAgentProfile: (req) => {
        seen.update.push(req);
        return {
          profile: create(AgentProfileSchema, {
            id: req.id,
            name: req.name,
            toolMapping: req.toolMapping,
            toolPolicy: req.toolPolicy,
          }),
        };
      },
    });
  });
}

describe('toolPolicy round trip', () => {
  it('create and update send PROFILE_ONLY and return it', async () => {
    const seen = { create: [] as CreateAgentProfileRequest[], update: [] as UpdateAgentProfileRequest[] };
    const transport = transportCapturingProfileWrites(seen);
    const created = await createLlmdAgentProfile({
      transport,
      tenantId: 'tenant-test',
      name: 'Strict',
      toolPolicy: ToolPolicy.PROFILE_ONLY,
    });
    expect(seen.create[0].toolPolicy).toBe(ToolPolicy.PROFILE_ONLY);
    expect(created.toolPolicy).toBe(ToolPolicy.PROFILE_ONLY);
    const updated = await updateLlmdAgentProfile({
      transport,
      tenantId: 'tenant-test',
      id: 'dyn-1',
      name: 'Strict',
      toolPolicy: ToolPolicy.PROFILE_ONLY,
    });
    expect(seen.update[0].toolPolicy).toBe(ToolPolicy.PROFILE_ONLY);
    expect(updated.toolPolicy).toBe(ToolPolicy.PROFILE_ONLY);
  });

  it('defaults to UNSPECIFIED when omitted', async () => {
    const seen = { create: [] as CreateAgentProfileRequest[], update: [] as UpdateAgentProfileRequest[] };
    const out = await createLlmdAgentProfile({
      transport: transportCapturingProfileWrites(seen),
      tenantId: 'tenant-test',
      name: 'Plain',
    });
    expect(out.toolPolicy).toBe(ToolPolicy.UNSPECIFIED);
  });
});

describe('outbound toolMapping on create/update', () => {
  const mapping = [
    { toolName: 'write_code', enabled: false },
    { toolName: 'search', enabled: true },
  ];

  it('createLlmdAgentProfile emits enabled=false explicitly, never dropping the mapping', async () => {
    const seen = { create: [] as CreateAgentProfileRequest[], update: [] as UpdateAgentProfileRequest[] };
    const out = await createLlmdAgentProfile({
      transport: transportCapturingProfileWrites(seen),
      tenantId: 'tenant-test',
      name: 'Ask',
      toolMapping: mapping,
    });
    expect(seen.create).toHaveLength(1);
    const req = seen.create[0];
    expect(req.toolMapping).toHaveLength(2);
    expect(req.toolMapping[0].toolName).toBe('write_code');
    expect(req.toolMapping[0].enabled).toBe(false);
    expect(req.toolMapping[1].toolName).toBe('search');
    expect(req.toolMapping[1].enabled).toBe(true);
    // Round trip through the stub keeps the explicit false on the way back too.
    expect(out.toolMapping[0].enabled).toBe(false);
  });

  it('updateLlmdAgentProfile emits enabled=false explicitly, never dropping the mapping', async () => {
    const seen = { create: [] as CreateAgentProfileRequest[], update: [] as UpdateAgentProfileRequest[] };
    const out = await updateLlmdAgentProfile({
      transport: transportCapturingProfileWrites(seen),
      tenantId: 'tenant-test',
      id: 'dyn-1',
      name: 'Ask',
      toolMapping: mapping,
    });
    expect(seen.update).toHaveLength(1);
    const req = seen.update[0];
    expect(req.id).toBe('dyn-1');
    expect(req.toolMapping).toHaveLength(2);
    expect(req.toolMapping[0].toolName).toBe('write_code');
    expect(req.toolMapping[0].enabled).toBe(false);
    expect(req.toolMapping[1].toolName).toBe('search');
    expect(req.toolMapping[1].enabled).toBe(true);
    expect(out.toolMapping[0].enabled).toBe(false);
  });
});

/** Stub LlmService that records the CreateSessionFromAgentProfile request it receives. */
function transportCapturing(seen: CreateSessionFromAgentProfileRequest[]) {
  return createRouterTransport(({ service }) => {
    service(LlmService, {
      createSessionFromAgentProfile: (req) => {
        seen.push(req);
        return { sessionId: req.sessionId, agentProfileId: req.agentProfileId };
      },
    });
  });
}

describe('createLlmdSessionFromAgentProfile', () => {
  it('sends only the required fields when no overrides are given', async () => {
    const seen: CreateSessionFromAgentProfileRequest[] = [];
    const res = await createLlmdSessionFromAgentProfile({
      transport: transportCapturing(seen),
      tenantId: 'tenant-test',
      agentProfileId: ' static-ask ',
      sessionId: ' sess-1 ',
    });
    expect(seen).toHaveLength(1);
    const req = seen[0];
    expect(req.agentProfileId).toBe('static-ask');
    expect(req.sessionId).toBe('sess-1');
    expect(req.displayName).toBeUndefined();
    expect(req.llmProviderIdOverride).toBeUndefined();
    expect(req.defaultModelOverride).toBeUndefined();
    expect(req.chatSurface).toBeUndefined();
    expect(req.additionalTools).toEqual([]);
    // (c) the wrapper returns the stub's response verbatim.
    expect(res).toEqual({ sessionId: 'sess-1', agentProfileId: 'static-ask' });
  });

  it('sends every override verbatim when supplied', async () => {
    const seen: CreateSessionFromAgentProfileRequest[] = [];
    const tool: ClientTool = {
      name: 'lookup_ticket',
      description: 'Fetch a ticket',
      parameters: { type: 'object', properties: { id: { type: 'string' } } },
    };
    const res = await createLlmdSessionFromAgentProfile({
      transport: transportCapturing(seen),
      tenantId: 'tenant-test',
      agentProfileId: 'static-ask',
      sessionId: 'sess-2',
      displayName: 'My session',
      llmProviderIdOverride: 'openai',
      defaultModelOverride: 'gpt-5',
      chatSurface: 'build',
      additionalTools: [tool],
    });
    const req = seen[0];
    expect(req.displayName).toBe('My session');
    expect(req.llmProviderIdOverride).toBe('openai');
    expect(req.defaultModelOverride).toBe('gpt-5');
    expect(req.chatSurface).toBe('build');
    expect(req.additionalTools).toHaveLength(1);
    expect(req.additionalTools[0].name).toBe('lookup_ticket');
    expect(req.additionalTools[0].description).toBe('Fetch a ticket');
    expect(res).toEqual({ sessionId: 'sess-2', agentProfileId: 'static-ask' });
  });

  it('drops a blank chatSurface rather than sending an empty string', async () => {
    const seen: CreateSessionFromAgentProfileRequest[] = [];
    await createLlmdSessionFromAgentProfile({
      transport: transportCapturing(seen),
      tenantId: 'tenant-test',
      agentProfileId: 'static-ask',
      sessionId: 'sess-3',
      chatSurface: '   ',
    });
    expect(seen[0].chatSurface).toBeUndefined();
  });
});

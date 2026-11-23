/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Agent profiles: named, reusable session configurations (system prompt
 * sections, provider/model defaults, memory flag, tool mapping) that a caller
 * can materialize into an ordinary session with one call.
 *
 * Every helper here is a thin wrapper over one LlmService RPC, written once
 * against Connect's Transport via the shared `callUnary` seam — the same shape
 * as `sessionAttachments.ts`.
 */

import { callUnary, scopeHeaders } from "./transport.js";
import { resolveLlmServiceClient, type LlmdCallTarget } from "./callTarget.js";
import { clientToolToRegisterProto, type ClientTool } from "./llm.js";
import {
  systemPromptSectionsToProto,
  type SystemPromptSectionWire,
} from "./systemPromptSectionsWire.js";
import { grpcSafeString } from "./utf8.js";
import {
  AgentProfileOverlayField,
  ToolPolicy,
  type AgentProfile,
  type AgentProfileOverlay,
  type ToolMapping,
} from "./gen/llmv1/llm_pb.js";

export { AgentProfileOverlayField, ToolPolicy };

type BaseOpts = LlmdCallTarget & {
  /** Tenant id — carried as the `llmd-tenant-id` header. */
  tenantId: string;
  /** Optional scope id — carried as `llmd-scope-id` when non-empty. */
  scopeId?: string;
  principalId?: string;
};

/** One entry of a profile's tool mapping (matches llm.v1.ToolMapping). */
export type LlmdAgentProfileToolMapping = {
  toolName: string;
  /**
   * true  → the server-wide tool is visible in sessions created from the profile.
   * false → hidden by default; an explicit session registration makes it
   *         visible again. Core session tools can never be disabled.
   */
  enabled: boolean;
};

/** An agent profile as seen by SDK consumers (matches llm.v1.AgentProfile). */
export type LlmdAgentProfile = {
  id: string;
  name: string;
  description: string;
  systemPromptSections: SystemPromptSectionWire[];
  /** Unset when the profile does not pin a provider. */
  llmProviderId?: string;
  /** Unset when the profile does not pin a model. */
  defaultModel?: string;
  memoryEnabled: boolean;
  toolMapping: LlmdAgentProfileToolMapping[];
  /** UNSPECIFIED is treated by the server as INHERIT_SERVER_WIDE. */
  toolPolicy: ToolPolicy;
  /** RFC3339; empty for static (code-defined) profiles. */
  createdAt: string;
  /** RFC3339; empty for static (code-defined) profiles. */
  updatedAt: string;
  /** Static profiles are defined in code and are read-only. */
  isStatic: boolean;
  /**
   * The fields the workspace's overlay changed (static profiles only); empty
   * when the profile is exactly its code definition.
   */
  customized: AgentProfileOverlayField[];
};

/** Map a generated llm.v1.AgentProfile message onto the SDK-facing shape. */
export function agentProfileFromProto(p: AgentProfile): LlmdAgentProfile {
  return {
    id: p.id,
    name: p.name,
    description: p.description,
    systemPromptSections: (p.systemPromptSections ?? []).map((s) => ({
      id: s.id,
      content: s.content,
      tags: [...(s.tags ?? [])],
    })),
    llmProviderId: p.llmProviderId,
    defaultModel: p.defaultModel,
    memoryEnabled: p.memoryEnabled === true,
    toolMapping: (p.toolMapping ?? []).map(toolMappingFromProto),
    toolPolicy: p.toolPolicy ?? ToolPolicy.UNSPECIFIED,
    createdAt: p.createdAt,
    updatedAt: p.updatedAt,
    isStatic: p.isStatic === true,
    customized: [...(p.customized ?? [])],
  };
}

function toolMappingFromProto(m: ToolMapping): LlmdAgentProfileToolMapping {
  // Explicit boolean copy: a truthiness check here would silently turn a
  // disabled tool into an enabled one downstream.
  return { toolName: m.toolName, enabled: m.enabled === true };
}

function toolMappingToProto(
  mapping: readonly LlmdAgentProfileToolMapping[] | undefined
): { toolName: string; enabled: boolean }[] {
  return (mapping ?? []).map((m) => ({
    toolName: grpcSafeString(m.toolName),
    enabled: m.enabled === true,
  }));
}

/** List every agent profile visible to the workspace (static tier included). */
export async function listLlmdAgentProfiles(
  opts: BaseOpts
): Promise<LlmdAgentProfile[]> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) => client.listAgentProfiles({}, o),
    scopeHeaders(opts),
    undefined,
    { label: "listAgentProfiles" }
  );
  return (res.profiles ?? []).map(agentProfileFromProto);
}

/** Fetch one agent profile by id. Rejects (NotFound) when it does not exist. */
export async function getLlmdAgentProfile(
  opts: BaseOpts & { profileId: string }
): Promise<LlmdAgentProfile> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) => client.getAgentProfile({ id: grpcSafeString(opts.profileId.trim()) }, o),
    scopeHeaders(opts),
    undefined,
    { label: "getAgentProfile" }
  );
  if (!res.profile) {
    throw new Error(`getAgentProfile: server returned no profile for id ${opts.profileId}`);
  }
  return agentProfileFromProto(res.profile);
}

/** Fields shared by create and update. */
export type LlmdAgentProfileSpec = {
  name: string;
  description?: string;
  systemPromptSections?: SystemPromptSectionWire[];
  llmProviderId?: string;
  defaultModel?: string;
  memoryEnabled?: boolean;
  toolMapping?: LlmdAgentProfileToolMapping[];
  /** Omitted = UNSPECIFIED (server treats as INHERIT_SERVER_WIDE). */
  toolPolicy?: ToolPolicy;
};

export type CreateLlmdAgentProfileOptions = BaseOpts &
  LlmdAgentProfileSpec & {
    /**
     * Caller-supplied id (e.g. a plugin/agent id). When absent the server
     * generates one. Fails AlreadyExists if the id is taken.
     */
    id?: string;
  };

/** Create a dynamic agent profile. */
export async function createLlmdAgentProfile(
  opts: CreateLlmdAgentProfileOptions
): Promise<LlmdAgentProfile> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.createAgentProfile(
        {
          name: grpcSafeString(opts.name),
          description: grpcSafeString(opts.description ?? ""),
          systemPromptSections: opts.systemPromptSections?.length
            ? systemPromptSectionsToProto(opts.systemPromptSections)
            : [],
          ...(opts.llmProviderId
            ? { llmProviderId: grpcSafeString(opts.llmProviderId) }
            : {}),
          ...(opts.defaultModel
            ? { defaultModel: grpcSafeString(opts.defaultModel) }
            : {}),
          memoryEnabled: opts.memoryEnabled === true,
          toolMapping: toolMappingToProto(opts.toolMapping),
          toolPolicy: opts.toolPolicy ?? ToolPolicy.UNSPECIFIED,
          ...(opts.id?.trim() ? { id: grpcSafeString(opts.id.trim()) } : {}),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "createAgentProfile" }
  );
  if (!res.profile) {
    throw new Error("createAgentProfile: server returned no profile");
  }
  return agentProfileFromProto(res.profile);
}

export type UpdateLlmdAgentProfileOptions = BaseOpts &
  LlmdAgentProfileSpec & { id: string };

/**
 * Wholesale replace of a stored (dynamic) profile: every field is overwritten
 * with the value given here; omitted optionals clear the stored value.
 */
export async function updateLlmdAgentProfile(
  opts: UpdateLlmdAgentProfileOptions
): Promise<LlmdAgentProfile> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) =>
      client.updateAgentProfile(
        {
          id: grpcSafeString(opts.id.trim()),
          name: grpcSafeString(opts.name),
          description: grpcSafeString(opts.description ?? ""),
          systemPromptSections: opts.systemPromptSections?.length
            ? systemPromptSectionsToProto(opts.systemPromptSections)
            : [],
          ...(opts.llmProviderId
            ? { llmProviderId: grpcSafeString(opts.llmProviderId) }
            : {}),
          ...(opts.defaultModel
            ? { defaultModel: grpcSafeString(opts.defaultModel) }
            : {}),
          memoryEnabled: opts.memoryEnabled === true,
          toolMapping: toolMappingToProto(opts.toolMapping),
          toolPolicy: opts.toolPolicy ?? ToolPolicy.UNSPECIFIED,
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "updateAgentProfile" }
  );
  if (!res.profile) {
    throw new Error(`updateAgentProfile: server returned no profile for id ${opts.id}`);
  }
  return agentProfileFromProto(res.profile);
}

/** Delete a dynamic agent profile. Static profiles are refused by the server. */
export async function deleteLlmdAgentProfile(
  opts: BaseOpts & { profileId: string }
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) => client.deleteAgentProfile({ id: grpcSafeString(opts.profileId.trim()) }, o),
    scopeHeaders(opts),
    undefined,
    { label: "deleteAgentProfile" }
  );
}

/**
 * A workspace's changes to one static profile (matches
 * llm.v1.AgentProfileOverlay). Unset optionals keep the base's value.
 */
export type LlmdAgentProfileOverlay = {
  baseId: string;
  llmProviderId?: string;
  defaultModel?: string;
  memoryEnabled?: boolean;
  /** Each entry sets that tool on or off; tools not named keep the base's setting. */
  toolOverrides: LlmdAgentProfileToolMapping[];
  /** Replace the content of the base section with the same id. */
  sectionReplacements: SystemPromptSectionWire[];
  /** Appended after the base's sections; ids must not match a base section. */
  extraSections: SystemPromptSectionWire[];
  /** RFC3339; empty when nothing is stored. */
  updatedAt: string;
};

function sectionsFromProto(
  sections: AgentProfileOverlay["extraSections"] | undefined
): SystemPromptSectionWire[] {
  return (sections ?? []).map((s) => ({ id: s.id, content: s.content, tags: [...(s.tags ?? [])] }));
}

/** Map a generated llm.v1.AgentProfileOverlay onto the SDK-facing shape. */
export function agentProfileOverlayFromProto(o: AgentProfileOverlay): LlmdAgentProfileOverlay {
  return {
    baseId: o.baseId,
    llmProviderId: o.llmProviderId,
    defaultModel: o.defaultModel,
    memoryEnabled: o.memoryEnabled,
    toolOverrides: (o.toolOverrides ?? []).map(toolMappingFromProto),
    sectionReplacements: sectionsFromProto(o.sectionReplacements),
    extraSections: sectionsFromProto(o.extraSections),
    updatedAt: o.updatedAt,
  };
}

/**
 * Read the workspace's overlay on a static profile, plus the base (the code
 * definition without the overlay). The overlay is empty when none is stored.
 * Rejects NotFound when profileId is not a static profile.
 */
export async function getLlmdAgentProfileOverlay(
  opts: BaseOpts & { profileId: string }
): Promise<{ overlay: LlmdAgentProfileOverlay; base: LlmdAgentProfile }> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) => client.getAgentProfileOverlay({ baseId: grpcSafeString(opts.profileId.trim()) }, o),
    scopeHeaders(opts),
    undefined,
    { label: "getAgentProfileOverlay" }
  );
  if (!res.overlay || !res.base) {
    throw new Error(`getAgentProfileOverlay: incomplete response for id ${opts.profileId}`);
  }
  return { overlay: agentProfileOverlayFromProto(res.overlay), base: agentProfileFromProto(res.base) };
}

/**
 * Replace the workspace's overlay on a static profile wholesale. Returns the
 * stored overlay and the merged profile.
 */
export async function setLlmdAgentProfileOverlay(
  opts: BaseOpts & { overlay: Omit<LlmdAgentProfileOverlay, "updatedAt"> }
): Promise<{ overlay: LlmdAgentProfileOverlay; profile: LlmdAgentProfile }> {
  const client = resolveLlmServiceClient(opts);
  const ov = opts.overlay;
  const res = await callUnary(
    (o) =>
      client.setAgentProfileOverlay(
        {
          overlay: {
            baseId: grpcSafeString(ov.baseId.trim()),
            ...(ov.llmProviderId !== undefined ? { llmProviderId: grpcSafeString(ov.llmProviderId) } : {}),
            ...(ov.defaultModel !== undefined ? { defaultModel: grpcSafeString(ov.defaultModel) } : {}),
            ...(ov.memoryEnabled !== undefined ? { memoryEnabled: ov.memoryEnabled === true } : {}),
            toolOverrides: toolMappingToProto(ov.toolOverrides),
            sectionReplacements: ov.sectionReplacements.length
              ? systemPromptSectionsToProto(ov.sectionReplacements)
              : [],
            extraSections: ov.extraSections.length ? systemPromptSectionsToProto(ov.extraSections) : [],
          },
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "setAgentProfileOverlay" }
  );
  if (!res.overlay || !res.profile) {
    throw new Error(`setAgentProfileOverlay: incomplete response for id ${ov.baseId}`);
  }
  return { overlay: agentProfileOverlayFromProto(res.overlay), profile: agentProfileFromProto(res.profile) };
}

/** Remove the workspace's overlay, returning the static profile to its code definition. */
export async function resetLlmdAgentProfileOverlay(
  opts: BaseOpts & { profileId: string }
): Promise<LlmdAgentProfile> {
  const client = resolveLlmServiceClient(opts);
  const res = await callUnary(
    (o) => client.resetAgentProfileOverlay({ baseId: grpcSafeString(opts.profileId.trim()) }, o),
    scopeHeaders(opts),
    undefined,
    { label: "resetAgentProfileOverlay" }
  );
  if (!res.profile) {
    throw new Error(`resetAgentProfileOverlay: server returned no profile for id ${opts.profileId}`);
  }
  return agentProfileFromProto(res.profile);
}

export type CreateLlmdSessionFromAgentProfileOptions = BaseOpts & {
  agentProfileId: string;
  sessionId: string;
  displayName?: string;
  /** Per-session override of the profile's provider default. */
  llmProviderIdOverride?: string;
  /** Per-session override of the profile's model default. */
  defaultModelOverride?: string;
  /**
   * Extra session-scoped tools registered on top of the profile's enabled
   * tool mapping. A tool named here that the profile disables is visible
   * (explicit session registration wins).
   */
  additionalTools?: ClientTool[];
  /** Session surface / agent type (build, ask, debug, doc, pair). */
  chatSurface?: string;
};

/**
 * Create (or idempotently ensure) a session whose configuration is derived
 * from an agent profile. The result is an ordinary session — equivalent to an
 * ensureLlmdSession call built from the profile's fields plus the overrides.
 */
export async function createLlmdSessionFromAgentProfile(
  opts: CreateLlmdSessionFromAgentProfileOptions
): Promise<{ sessionId: string; agentProfileId: string }> {
  const client = resolveLlmServiceClient(opts);
  const chatSurface = opts.chatSurface?.trim();
  const res = await callUnary(
    (o) =>
      client.createSessionFromAgentProfile(
        {
          agentProfileId: grpcSafeString(opts.agentProfileId.trim()),
          sessionId: grpcSafeString(opts.sessionId.trim()),
          ...(opts.displayName
            ? { displayName: grpcSafeString(opts.displayName) }
            : {}),
          ...(opts.llmProviderIdOverride
            ? { llmProviderIdOverride: grpcSafeString(opts.llmProviderIdOverride) }
            : {}),
          ...(opts.defaultModelOverride
            ? { defaultModelOverride: grpcSafeString(opts.defaultModelOverride) }
            : {}),
          additionalTools: (opts.additionalTools ?? []).map(clientToolToRegisterProto),
          ...(chatSurface ? { chatSurface: grpcSafeString(chatSurface) } : {}),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "createSessionFromAgentProfile" }
  );
  return { sessionId: res.sessionId, agentProfileId: res.agentProfileId };
}

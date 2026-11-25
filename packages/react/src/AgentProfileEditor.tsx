/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Edits one agent profile: name, description, instructions, any additional
 * system prompt sections, provider + model (catalog picker), memory, tool
 * mapping and tool policy. Name and description reach the model too: llmd puts
 * them in the identity section of every session created from the profile.
 * Instructions are the section with id INSTRUCTIONS_SECTION_ID.
 * Static (code-defined) profiles render read-only. Transport-agnostic: the
 * host supplies the available tools (ListTools), the model options and the
 * save callback.
 */

import { useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import type { LlmdAgentProfile, LlmdAgentProfileSpec } from '@pyxis-llm/client';
import { CatalogModelPicker } from './CatalogModelPicker.js';
import type { ChatModelOption } from './chatModelOptions.js';

/** Numeric values of llm.v1.ToolPolicy (kept local so no runtime client import is needed). */
export const AGENT_TOOL_POLICY = { UNSPECIFIED: 0, INHERIT_SERVER_WIDE: 1, PROFILE_ONLY: 2 } as const;

/** Id of the system prompt section the editor's Instructions box reads and writes. */
export const INSTRUCTIONS_SECTION_ID = 'agent.instructions';

/**
 * Reserved by llmd for the section it builds from the profile's name and
 * description (internal/agentprofile.IdentitySectionID); profiles may not use it.
 */
export const IDENTITY_SECTION_ID = 'agent.identity';

type SectionDraft = { id: string; content: string; tags: string[] };

/**
 * The sections to save: instructions first (when set), then the additional
 * sections with fully empty rows dropped. Returns an error message instead
 * when an additional section would be refused or silently lost.
 */
export function buildProfileSections(
  instructions: string,
  additional: SectionDraft[]
): { sections: SectionDraft[] } | { error: string } {
  const out: SectionDraft[] = [];
  if (instructions.trim()) out.push({ id: INSTRUCTIONS_SECTION_ID, content: instructions, tags: [] });
  for (const [i, s] of additional.entries()) {
    const id = s.id.trim();
    const hasContent = s.content.trim() !== '';
    if (!id && !hasContent) continue;
    if (!id) return { error: `Additional section ${i + 1} needs an id.` };
    if (id === INSTRUCTIONS_SECTION_ID || id === IDENTITY_SECTION_ID) {
      return { error: `Additional section ${i + 1}: the id "${id}" is reserved.` };
    }
    out.push({ ...s, id });
  }
  return { sections: out };
}

/** A tool the profile can map (e.g. from `ListTools`). */
export type AgentProfileToolOption = { name: string; description?: string };

export type AgentProfileEditorProps = {
  /** The profile to edit; omit to create a new one. */
  profile?: LlmdAgentProfile;
  tools: AgentProfileToolOption[];
  modelOptions: ChatModelOption[];
  modelsLoading?: boolean;
  /** Persist the spec (create when `profile` is absent, otherwise update). */
  onSave: (spec: LlmdAgentProfileSpec, profile?: LlmdAgentProfile) => Promise<void> | void;
  onCancel?: () => void;
};

type ToolState = 'default' | 'enabled' | 'disabled';

export function AgentProfileEditor({
  profile,
  tools,
  modelOptions,
  modelsLoading,
  onSave,
  onCancel,
}: AgentProfileEditorProps) {
  const readOnly = profile?.isStatic === true;
  const [name, setName] = useState(profile?.name ?? '');
  const [description, setDescription] = useState(profile?.description ?? '');
  const initialSections = profile?.systemPromptSections ?? [];
  const [instructions, setInstructions] = useState(
    () => initialSections.find((s) => s.id === INSTRUCTIONS_SECTION_ID)?.content ?? ''
  );
  const [sections, setSections] = useState<SectionDraft[]>(() =>
    initialSections
      .filter((s) => s.id !== INSTRUCTIONS_SECTION_ID)
      .map((s) => ({ ...s, tags: [...(s.tags ?? [])] }))
  );
  const [showAdditional, setShowAdditional] = useState(false);
  const [providerId, setProviderId] = useState<string | null>(profile?.llmProviderId || null);
  const [modelId, setModelId] = useState<string | null>(profile?.defaultModel || null);
  const [memory, setMemory] = useState(profile?.memoryEnabled === true);
  const [policy, setPolicy] = useState<number>(profile?.toolPolicy ?? 0);
  const [mapping, setMapping] = useState<Record<string, boolean>>(() =>
    Object.fromEntries((profile?.toolMapping ?? []).map((m) => [m.toolName, m.enabled]))
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const toolState = (n: string): ToolState =>
    n in mapping ? (mapping[n] ? 'enabled' : 'disabled') : 'default';

  const setToolState = (n: string, s: ToolState) =>
    setMapping((prev) => {
      const next = { ...prev };
      if (s === 'default') delete next[n];
      else next[n] = s === 'enabled';
      return next;
    });

  const updateSection = (i: number, patch: Partial<SectionDraft>) =>
    setSections((prev) => prev.map((s, j) => (j === i ? { ...s, ...patch } : s)));

  const submit = async () => {
    if (readOnly || !name.trim()) return;
    const built = buildProfileSections(instructions, sections);
    if ('error' in built) {
      setError(new Error(built.error));
      setShowAdditional(true);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await onSave(
        {
          name: name.trim(),
          description,
          systemPromptSections: built.sections,
          ...(providerId ? { llmProviderId: providerId } : {}),
          ...(modelId ? { defaultModel: modelId } : {}),
          memoryEnabled: memory,
          toolMapping: Object.entries(mapping).map(([toolName, enabled]) => ({ toolName, enabled })),
          toolPolicy: policy,
        },
        profile
      );
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Stack gap="sm" data-testid="agent-profile-editor">
      {readOnly ? (
        <Alert color="gray">This profile is defined in code and is read-only.</Alert>
      ) : null}
      {error ? (
        <Alert color="red" title="Save failed">
          {error.message}
        </Alert>
      ) : null}
      <TextInput
        label="Name"
        value={name}
        disabled={readOnly}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <Textarea
        label="Description"
        description="Shown in lists, and sent to the agent as a description of itself."
        value={description}
        disabled={readOnly}
        onChange={(e) => setDescription(e.currentTarget.value)}
      />
      <Textarea
        label="Instructions"
        description="What the agent does and how it should behave. Sent as its system prompt."
        autosize
        minRows={6}
        value={instructions}
        disabled={readOnly}
        onChange={(e) => setInstructions(e.currentTarget.value)}
      />

      <Stack gap="xs">
        <Group>
          <Button
            size="xs"
            variant="subtle"
            aria-expanded={showAdditional}
            onClick={() => setShowAdditional((v) => !v)}
          >
            {showAdditional ? 'Hide' : 'Show'} additional sections ({sections.length})
          </Button>
        </Group>
        <Collapse in={showAdditional}>
        <Stack gap="xs">
        {sections.map((s, i) => (
          <Paper key={i} withBorder p="xs" data-testid={`section-${i}`}>
            <Stack gap="xs">
              <TextInput
                label="Section id"
                value={s.id}
                disabled={readOnly}
                onChange={(e) => updateSection(i, { id: e.currentTarget.value })}
              />
              <Textarea
                label="Section content"
                autosize
                minRows={2}
                value={s.content}
                disabled={readOnly}
                onChange={(e) => updateSection(i, { content: e.currentTarget.value })}
              />
              {readOnly ? null : (
                <Button
                  size="xs"
                  variant="subtle"
                  color="red"
                  onClick={() => setSections((prev) => prev.filter((_, j) => j !== i))}
                >
                  Remove section
                </Button>
              )}
            </Stack>
          </Paper>
        ))}
        {readOnly ? null : (
          <Group>
            <Button
              size="xs"
              variant="default"
              onClick={() => setSections((prev) => [...prev, { id: '', content: '', tags: [] }])}
            >
              Add section
            </Button>
          </Group>
        )}
        </Stack>
        </Collapse>
      </Stack>

      <Stack gap={4}>
        <Text fw={600}>Provider and model</Text>
        {readOnly ? (
          <Text size="sm">
            {providerId && modelId ? `${providerId}: ${modelId}` : 'Not pinned'}
          </Text>
        ) : (
          <CatalogModelPicker
            options={modelOptions}
            loading={modelsLoading}
            selectedProviderId={providerId}
            selectedModelId={modelId}
            emptyLabel="Server default"
            tooltip="Model for sessions created from this profile"
            onSelect={(p, m) => {
              setProviderId(p);
              setModelId(m);
            }}
          />
        )}
        {!readOnly && providerId ? (
          <Group>
            <Button
              size="xs"
              variant="subtle"
              onClick={() => {
                setProviderId(null);
                setModelId(null);
              }}
            >
              Clear model
            </Button>
          </Group>
        ) : null}
      </Stack>

      <Switch
        label="Memory enabled"
        checked={memory}
        disabled={readOnly}
        onChange={(e) => setMemory(e.currentTarget.checked)}
      />

      <NativeSelect
        label="Tool policy"
        value={String(policy)}
        disabled={readOnly}
        onChange={(e) => setPolicy(Number(e.currentTarget.value))}
        data={[
          { value: String(AGENT_TOOL_POLICY.UNSPECIFIED), label: 'Server default' },
          { value: String(AGENT_TOOL_POLICY.INHERIT_SERVER_WIDE), label: 'Inherit server-wide tools' },
          { value: String(AGENT_TOOL_POLICY.PROFILE_ONLY), label: 'Profile tools only' },
        ]}
      />

      <Stack gap={4}>
        <Text fw={600}>Tool mapping</Text>
        {tools.length === 0 ? (
          <Text size="sm" c="dimmed">
            No tools available.
          </Text>
        ) : null}
        {tools.map((t) => (
          <Group key={t.name} justify="space-between" wrap="nowrap">
            <Checkbox
              readOnly
              checked={toolState(t.name) === 'enabled'}
              indeterminate={toolState(t.name) === 'default'}
              label={t.name}
              description={t.description}
              disabled={readOnly}
              onChange={() => undefined}
            />
            <NativeSelect
              aria-label={`Tool ${t.name}`}
              value={toolState(t.name)}
              disabled={readOnly}
              onChange={(e) => setToolState(t.name, e.currentTarget.value as ToolState)}
              data={[
                { value: 'default', label: 'Default' },
                { value: 'enabled', label: 'Enabled' },
                { value: 'disabled', label: 'Disabled' },
              ]}
            />
          </Group>
        ))}
      </Stack>

      <Group>
        {readOnly ? null : (
          <Button disabled={busy || !name.trim()} onClick={() => void submit()}>
            Save
          </Button>
        )}
        {onCancel ? (
          <Button variant="default" onClick={onCancel}>
            {readOnly ? 'Close' : 'Cancel'}
          </Button>
        ) : null}
      </Group>
    </Stack>
  );
}

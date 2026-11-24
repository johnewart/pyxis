/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 *
 * Customizes a static (code-defined) agent profile for one workspace. The
 * profile itself stays read-only and always follows its code definition; the
 * changes made here are stored as an llm.v1.AgentProfileOverlay and applied on
 * top of it. Every control shows the built-in value, so the user can see what
 * they changed. Transport-agnostic: the host loads, saves and resets the
 * overlay (getLlmdAgentProfileOverlay / setLlmdAgentProfileOverlay /
 * resetLlmdAgentProfileOverlay).
 */

import { useEffect, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import type { LlmdAgentProfile, LlmdAgentProfileOverlay } from '@pyxis-llm/client';
import { CatalogModelPicker } from './CatalogModelPicker.js';
import type { ChatModelOption } from './chatModelOptions.js';
import { IDENTITY_SECTION_ID, type AgentProfileToolOption } from './AgentProfileEditor.js';

/** The overlay as saved: everything but updatedAt. */
export type AgentProfileOverlayDraft = Omit<LlmdAgentProfileOverlay, 'updatedAt'>;

type Tri = 'inherit' | 'on' | 'off';
type Section = { id: string; content: string; tags: string[] };

/** Editor state, kept in one shape so buildAgentProfileOverlay can be tested alone. */
export type OverlayFormState = {
  /** null = use the built-in model. */
  model: { providerId: string | null; modelId: string | null } | null;
  memory: Tri;
  /** Tools the user set; absent = built-in setting. */
  tools: Record<string, boolean>;
  /** Built-in section id -> replacement content. */
  replacements: Record<string, string>;
  extras: Section[];
};

/** The form state for an existing overlay. */
export function overlayFormState(ov: LlmdAgentProfileOverlay): OverlayFormState {
  return {
    model:
      ov.llmProviderId !== undefined || ov.defaultModel !== undefined
        ? { providerId: ov.llmProviderId || null, modelId: ov.defaultModel || null }
        : null,
    memory: ov.memoryEnabled === undefined ? 'inherit' : ov.memoryEnabled ? 'on' : 'off',
    tools: Object.fromEntries(ov.toolOverrides.map((t) => [t.toolName, t.enabled])),
    replacements: Object.fromEntries(ov.sectionReplacements.map((s) => [s.id, s.content])),
    extras: ov.extraSections.map((s) => ({ id: s.id, content: s.content, tags: [...(s.tags ?? [])] })),
  };
}

/**
 * The overlay to save, or an error. Overrides equal to the built-in value are
 * dropped, so the overlay only ever holds real changes; empty extra sections
 * are dropped too.
 */
export function buildAgentProfileOverlay(
  base: LlmdAgentProfile,
  s: OverlayFormState
): { overlay: AgentProfileOverlayDraft } | { error: string } {
  const baseTools = new Map(base.toolMapping.map((m) => [m.toolName, m.enabled]));
  const baseSections = new Map(base.systemPromptSections.map((x) => [x.id, x.content]));
  const out: AgentProfileOverlayDraft = {
    baseId: base.id,
    toolOverrides: [],
    sectionReplacements: [],
    extraSections: [],
  };
  if (s.model) {
    const p = s.model.providerId ?? '';
    const m = s.model.modelId ?? '';
    if (p !== (base.llmProviderId ?? '') || m !== (base.defaultModel ?? '')) {
      out.llmProviderId = p;
      out.defaultModel = m;
    }
  }
  if (s.memory !== 'inherit' && (s.memory === 'on') !== base.memoryEnabled) {
    out.memoryEnabled = s.memory === 'on';
  }
  for (const [toolName, enabled] of Object.entries(s.tools)) {
    if (baseTools.get(toolName) === enabled) continue;
    // A tool the base does not map is off unless enabled, so "off" there is no change.
    if (!baseTools.has(toolName) && !enabled) continue;
    out.toolOverrides.push({ toolName, enabled });
  }
  for (const [id, content] of Object.entries(s.replacements)) {
    if (!baseSections.has(id) || baseSections.get(id) === content) continue;
    out.sectionReplacements.push({ id, content, tags: [] });
  }
  const seen = new Set<string>();
  for (const [i, x] of s.extras.entries()) {
    const id = x.id.trim();
    if (!id && !x.content.trim()) continue;
    if (!id) return { error: `Your section ${i + 1} needs an id.` };
    if (baseSections.has(id)) return { error: `Your section ${i + 1}: "${id}" is a built-in section; override it instead.` };
    if (id === IDENTITY_SECTION_ID) return { error: `Your section ${i + 1}: the id "${id}" is reserved.` };
    if (seen.has(id)) return { error: `Your section ${i + 1}: the id "${id}" is used twice.` };
    seen.add(id);
    out.extraSections.push({ ...x, id });
  }
  return { overlay: out };
}

export type AgentProfileOverlayEditorProps = {
  profileId: string;
  tools: AgentProfileToolOption[];
  modelOptions: ChatModelOption[];
  modelsLoading?: boolean;
  onLoad: (profileId: string) => Promise<{ overlay: LlmdAgentProfileOverlay; base: LlmdAgentProfile }>;
  onSave: (overlay: AgentProfileOverlayDraft) => Promise<void> | void;
  onReset: (profileId: string) => Promise<void> | void;
  onClose: () => void;
};

const changedBadge = (
  <Badge size="xs" variant="light" color="blue">
    changed
  </Badge>
);

export function AgentProfileOverlayEditor({
  profileId,
  tools,
  modelOptions,
  modelsLoading,
  onLoad,
  onSave,
  onReset,
  onClose,
}: AgentProfileOverlayEditorProps) {
  const [base, setBase] = useState<LlmdAgentProfile | null>(null);
  const [form, setForm] = useState<OverlayFormState | null>(null);
  const [stored, setStored] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    let cancelled = false;
    onLoad(profileId).then(
      (r) => {
        if (cancelled) return;
        setBase(r.base);
        setForm(overlayFormState(r.overlay));
        setStored(r.overlay.updatedAt !== '');
      },
      (e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e : new Error(String(e)));
      }
    );
    return () => {
      cancelled = true;
    };
  }, [profileId, onLoad]);

  if (!base || !form) {
    return error ? (
      <Stack gap="sm">
        <Alert color="red" title="Could not load the profile">
          {error.message}
        </Alert>
        <Group>
          <Button variant="default" onClick={onClose}>
            Close
          </Button>
        </Group>
      </Stack>
    ) : (
      <Loader size="sm" />
    );
  }

  const set = (patch: Partial<OverlayFormState>) => setForm((f) => (f ? { ...f, ...patch } : f));
  const built = buildAgentProfileOverlay(base, form);
  const overlay = 'overlay' in built ? built.overlay : null;
  const modelChanged = overlay?.defaultModel !== undefined || overlay?.llmProviderId !== undefined;
  const toolChanged = (n: string) => overlay?.toolOverrides.some((t) => t.toolName === n) ?? false;
  const baseTool = (n: string) => base.toolMapping.find((m) => m.toolName === n)?.enabled;
  const effectiveProvider = form.model ? form.model.providerId : base.llmProviderId || null;
  const effectiveModel = form.model ? form.model.modelId : base.defaultModel || null;
  // Every tool the host knows, plus any the base maps that the host does not list.
  const toolNames = [
    ...tools,
    ...base.toolMapping
      .filter((m) => !tools.some((t) => t.name === m.toolName))
      .map((m) => ({ name: m.toolName, description: undefined as string | undefined })),
  ];

  const run = async (fn: () => Promise<void> | void) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setBusy(false);
    }
  };

  const save = () => {
    if (!overlay) {
      setError(new Error('error' in built ? built.error : 'Invalid changes'));
      return;
    }
    void run(() => onSave(overlay));
  };

  return (
    <Stack gap="sm" data-testid="agent-profile-overlay-editor">
      <Group gap="xs">
        <Text fw={600}>{base.name}</Text>
        <Badge variant="light" color="gray">
          built in
        </Badge>
      </Group>
      <Alert color="gray">
        This agent is built in and updates with the app. Your changes below are kept separately and applied on top of
        it, so they survive updates. Anything you leave alone follows the built-in setting.
      </Alert>
      {error ? (
        <Alert color="red" title="Save failed">
          {error.message}
        </Alert>
      ) : null}
      {base.description ? (
        <Text size="sm" c="dimmed">
          {base.description}
        </Text>
      ) : null}

      <Stack gap={4}>
        <Group gap="xs">
          <Text fw={600}>Provider and model</Text>
          {modelChanged ? changedBadge : null}
        </Group>
        <CatalogModelPicker
          options={modelOptions}
          loading={modelsLoading}
          selectedProviderId={effectiveProvider}
          selectedModelId={effectiveModel}
          emptyLabel="Server default"
          tooltip="Model for sessions created from this agent"
          onSelect={(p, m) => set({ model: { providerId: p, modelId: m } })}
        />
        <Text size="xs" c="dimmed">
          Built in: {base.llmProviderId && base.defaultModel ? `${base.llmProviderId}: ${base.defaultModel}` : 'server default'}
        </Text>
        {form.model ? (
          <Group>
            <Button size="xs" variant="subtle" onClick={() => set({ model: null })}>
              Use built-in model
            </Button>
          </Group>
        ) : null}
      </Stack>

      <Group gap="xs" align="end">
        <NativeSelect
          label="Memory"
          value={form.memory}
          onChange={(e) => set({ memory: e.currentTarget.value as Tri })}
          data={[
            { value: 'inherit', label: `Built in (${base.memoryEnabled ? 'on' : 'off'})` },
            { value: 'on', label: 'On' },
            { value: 'off', label: 'Off' },
          ]}
        />
        {overlay?.memoryEnabled !== undefined ? changedBadge : null}
      </Group>

      <Stack gap={4}>
        <Text fw={600}>Tools</Text>
        {toolNames.map((t) => {
          const b = baseTool(t.name);
          const value = t.name in form.tools ? (form.tools[t.name] ? 'on' : 'off') : 'inherit';
          return (
            <Group key={t.name} justify="space-between" wrap="nowrap">
              <Stack gap={0}>
                <Group gap="xs">
                  <Text size="sm">{t.name}</Text>
                  {toolChanged(t.name) ? changedBadge : null}
                </Group>
                {t.description ? (
                  <Text size="xs" c="dimmed">
                    {t.description}
                  </Text>
                ) : null}
              </Stack>
              <NativeSelect
                aria-label={`Tool ${t.name}`}
                value={value}
                onChange={(e) => {
                  const v = e.currentTarget.value as Tri;
                  const next = { ...form.tools };
                  if (v === 'inherit') delete next[t.name];
                  else next[t.name] = v === 'on';
                  set({ tools: next });
                }}
                data={[
                  { value: 'inherit', label: `Built in (${b ? 'on' : 'off'})` },
                  { value: 'on', label: 'On' },
                  { value: 'off', label: 'Off' },
                ]}
              />
            </Group>
          );
        })}
      </Stack>

      <Stack gap="xs">
        <Text fw={600}>Instructions</Text>
        {base.systemPromptSections.map((s) => {
          const overridden = s.id in form.replacements;
          return (
            <Paper key={s.id} withBorder p="xs" data-testid={`base-section-${s.id}`}>
              <Stack gap="xs">
                <Group justify="space-between">
                  <Group gap="xs">
                    <Text size="sm" fw={500}>
                      {s.id}
                    </Text>
                    {overridden ? changedBadge : null}
                  </Group>
                  {overridden ? (
                    <Button
                      size="xs"
                      variant="subtle"
                      onClick={() => {
                        const next = { ...form.replacements };
                        delete next[s.id];
                        set({ replacements: next });
                      }}
                    >
                      Use built-in text
                    </Button>
                  ) : (
                    <Button
                      size="xs"
                      variant="subtle"
                      onClick={() => set({ replacements: { ...form.replacements, [s.id]: s.content } })}
                    >
                      Override
                    </Button>
                  )}
                </Group>
                {overridden ? (
                  <Textarea
                    aria-label={`Your text for ${s.id}`}
                    autosize
                    minRows={4}
                    value={form.replacements[s.id]}
                    onChange={(e) => set({ replacements: { ...form.replacements, [s.id]: e.currentTarget.value } })}
                  />
                ) : (
                  <Text size="sm" c="dimmed" style={{ whiteSpace: 'pre-wrap' }} lineClamp={6}>
                    {s.content}
                  </Text>
                )}
                {overridden ? (
                  <Text size="xs" c="dimmed">
                    Overriding stops this section from picking up changes to the built-in text.
                  </Text>
                ) : null}
              </Stack>
            </Paper>
          );
        })}
        {form.extras.map((x, i) => (
          <Paper key={i} withBorder p="xs" data-testid={`extra-section-${i}`}>
            <Stack gap="xs">
              <Group gap="xs">
                <Text size="sm" fw={500}>
                  Your section
                </Text>
                {changedBadge}
              </Group>
              <TextInput
                label="Section id"
                value={x.id}
                onChange={(e) =>
                  set({ extras: form.extras.map((y, j) => (j === i ? { ...y, id: e.currentTarget.value } : y)) })
                }
              />
              <Textarea
                label="Section content"
                autosize
                minRows={2}
                value={x.content}
                onChange={(e) =>
                  set({ extras: form.extras.map((y, j) => (j === i ? { ...y, content: e.currentTarget.value } : y)) })
                }
              />
              <Group>
                <Button
                  size="xs"
                  variant="subtle"
                  color="red"
                  onClick={() => set({ extras: form.extras.filter((_, j) => j !== i) })}
                >
                  Remove section
                </Button>
              </Group>
            </Stack>
          </Paper>
        ))}
        <Group>
          <Button
            size="xs"
            variant="default"
            onClick={() => set({ extras: [...form.extras, { id: '', content: '', tags: [] }] })}
          >
            Add section
          </Button>
        </Group>
      </Stack>

      <Group>
        <Button disabled={busy} onClick={save}>
          Save changes
        </Button>
        <Button variant="default" disabled={busy} onClick={onClose}>
          Cancel
        </Button>
        <Button
          variant="subtle"
          color="red"
          disabled={busy || !stored}
          onClick={() => void run(() => onReset(base.id))}
        >
          Reset to built-in
        </Button>
      </Group>
    </Stack>
  );
}

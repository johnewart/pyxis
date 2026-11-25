/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Lists llmd providers with expandable catalog models, a refresh action, and
 * add / edit / remove flows built on ProviderConnectForm. Transport-agnostic:
 * the host wires the callbacks to its @pyxis-llm/client calls.
 */

import { useState } from 'react';
import { Alert, Badge, Button, Collapse, Group, Paper, Stack, Text } from '@mantine/core';
import type { CatalogModelSummaryLike, LlmProviderSummaryLike } from './modelCatalogSource.js';
import { ProviderConnectForm, type ProviderConnectConfig } from './ProviderConnectForm.js';

export type ProviderListProps = {
  providers: LlmProviderSummaryLike[];
  models: CatalogModelSummaryLike[];
  /** Re-scan a provider's catalog (e.g. `refreshProvider(id)`). */
  onRefresh: (providerId: string) => Promise<void> | void;
  /** Register a new provider (e.g. `registerProvider`). */
  onAdd: (providerId: string, config: ProviderConnectConfig) => Promise<void> | void;
  /** Update an existing provider (e.g. `updateProviderConfig`). */
  onUpdate: (providerId: string, config: ProviderConnectConfig) => Promise<void> | void;
  /** Remove a provider. */
  onRemove: (providerId: string) => Promise<void> | void;
  /** Load a provider's config for editing (e.g. `getProviderConfig`). */
  loadConfig: (providerId: string) => Promise<ProviderConnectConfig>;
};

type Editing = { mode: 'create' } | { mode: 'edit'; id: string; config: ProviderConnectConfig };

export function ProviderList({
  providers,
  models,
  onRefresh,
  onAdd,
  onUpdate,
  onRemove,
  loadConfig,
}: ProviderListProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<Editing | null>(null);
  const [newId, setNewId] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const run = async (fn: () => Promise<void> | void): Promise<boolean> => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      return true;
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const startEdit = async (id: string) => {
    await run(async () => {
      const config = await loadConfig(id);
      setEditing({ mode: 'edit', id, config });
    });
  };

  const submit = async (config: ProviderConnectConfig) => {
    if (!editing) return;
    const ok = await run(() =>
      editing.mode === 'create' ? onAdd(newId.trim(), config) : onUpdate(editing.id, config)
    );
    if (ok) {
      setEditing(null);
      setNewId('');
    }
  };

  return (
    <Stack gap="sm">
      {error && !editing ? (
        <Alert color="red" title="Provider action failed">
          {error.message}
        </Alert>
      ) : null}
      {providers.length === 0 ? <Text c="dimmed">No providers configured.</Text> : null}
      {providers.map((p) => {
        const pModels = models.filter((m) => m.providerId === p.id);
        const open = expanded.has(p.id);
        return (
          <Paper key={p.id} withBorder p="sm" data-testid={`provider-${p.id}`}>
            <Group justify="space-between">
              <Group gap="xs">
                <Text fw={600}>{p.id}</Text>
                <Badge variant="light">{p.type}</Badge>
                <Text size="sm" c="dimmed">
                  {pModels.length || p.catalogModelCount} models
                </Text>
                <Text size="sm" c="dimmed" data-testid={`refreshed-${p.id}`}>
                  {p.modelsRefreshedUnixMs
                    ? `refreshed ${new Date(p.modelsRefreshedUnixMs).toLocaleString()}`
                    : 'never refreshed'}
                </Text>
              </Group>
              <Group gap="xs">
                <Button size="xs" variant="subtle" onClick={() => toggle(p.id)}>
                  {open ? 'Hide models' : 'Show models'}
                </Button>
                <Button size="xs" variant="subtle" disabled={busy} onClick={() => void run(() => onRefresh(p.id))}>
                  Refresh
                </Button>
                {p.userRegistered ? (
                  <>
                    <Button size="xs" variant="subtle" disabled={busy} onClick={() => void startEdit(p.id)}>
                      Edit
                    </Button>
                    <Button
                      size="xs"
                      variant="subtle"
                      color="red"
                      disabled={busy}
                      onClick={() => void run(() => onRemove(p.id))}
                    >
                      Remove
                    </Button>
                  </>
                ) : null}
              </Group>
            </Group>
            <Collapse in={open}>
              <Stack gap={2} mt="xs">
                {pModels.length === 0 ? (
                  <Text size="sm" c="dimmed">
                    No models in catalog.
                  </Text>
                ) : (
                  pModels.map((m) => (
                    <Text key={m.modelId} size="sm">
                      {(m.displayName ?? '').trim() || m.modelId}
                    </Text>
                  ))
                )}
              </Stack>
            </Collapse>
          </Paper>
        );
      })}

      {editing ? (
        <Paper withBorder p="sm">
          <ProviderConnectForm
            mode={editing.mode}
            providerId={editing.mode === 'edit' ? editing.id : newId}
            onProviderIdChange={setNewId}
            initialConfig={editing.mode === 'edit' ? editing.config : undefined}
            onSubmit={submit}
            submitting={busy}
            error={error}
          />
          <Button mt="xs" variant="default" onClick={() => setEditing(null)}>
            Cancel
          </Button>
        </Paper>
      ) : (
        <Group>
          <Button
            onClick={() => {
              setError(null);
              setEditing({ mode: 'create' });
            }}
          >
            Add provider
          </Button>
        </Group>
      )}
    </Stack>
  );
}

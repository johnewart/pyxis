/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Lists agent profiles with create / edit / delete flows built on
 * AgentProfileEditor. Static profiles open read-only and cannot be deleted.
 * Transport-agnostic: the host wires callbacks to its @pyxis-llm/client calls.
 */

import { useState } from 'react';
import { Alert, Badge, Button, Group, Paper, Stack, Text } from '@mantine/core';
import type { LlmdAgentProfile, LlmdAgentProfileSpec } from '@pyxis-llm/client';
import { AgentProfileEditor, type AgentProfileToolOption } from './AgentProfileEditor.js';
import type { ChatModelOption } from './chatModelOptions.js';

export type AgentProfileListProps = {
  profiles: LlmdAgentProfile[];
  tools: AgentProfileToolOption[];
  modelOptions: ChatModelOption[];
  modelsLoading?: boolean;
  /** e.g. `createLlmdAgentProfile`. */
  onCreate: (spec: LlmdAgentProfileSpec) => Promise<void> | void;
  /** e.g. `updateLlmdAgentProfile`. */
  onUpdate: (id: string, spec: LlmdAgentProfileSpec) => Promise<void> | void;
  /** e.g. `deleteLlmdAgentProfile`. */
  onDelete: (id: string) => Promise<void> | void;
};

type Editing = { mode: 'create' } | { mode: 'edit'; profile: LlmdAgentProfile };

export function AgentProfileList({
  profiles,
  tools,
  modelOptions,
  modelsLoading,
  onCreate,
  onUpdate,
  onDelete,
}: AgentProfileListProps) {
  const [editing, setEditing] = useState<Editing | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const remove = async (id: string) => {
    setBusy(true);
    setError(null);
    try {
      await onDelete(id);
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setBusy(false);
    }
  };

  const save = async (spec: LlmdAgentProfileSpec) => {
    if (!editing) return;
    if (editing.mode === 'create') await onCreate(spec);
    else await onUpdate(editing.profile.id, spec);
    setEditing(null);
  };

  if (editing) {
    return (
      <AgentProfileEditor
        key={editing.mode === 'edit' ? editing.profile.id : 'new'}
        profile={editing.mode === 'edit' ? editing.profile : undefined}
        tools={tools}
        modelOptions={modelOptions}
        modelsLoading={modelsLoading}
        onSave={save}
        onCancel={() => setEditing(null)}
      />
    );
  }

  return (
    <Stack gap="sm">
      {error ? (
        <Alert color="red" title="Profile action failed">
          {error.message}
        </Alert>
      ) : null}
      {profiles.length === 0 ? <Text c="dimmed">No agent profiles.</Text> : null}
      {profiles.map((p) => (
        <Paper key={p.id} withBorder p="sm" data-testid={`agent-profile-${p.id}`}>
          <Group justify="space-between">
            <Stack gap={0}>
              <Group gap="xs">
                <Text fw={600}>{p.name}</Text>
                {p.isStatic ? (
                  <Badge variant="light" color="gray">
                    static
                  </Badge>
                ) : null}
              </Group>
              {p.description ? (
                <Text size="sm" c="dimmed">
                  {p.description}
                </Text>
              ) : null}
            </Stack>
            <Group gap="xs">
              <Button
                size="xs"
                variant="subtle"
                aria-label={`${p.isStatic ? 'View' : 'Edit'} ${p.name}`}
                onClick={() => setEditing({ mode: 'edit', profile: p })}
              >
                {p.isStatic ? 'View' : 'Edit'}
              </Button>
              {p.isStatic ? null : (
                <Button
                  size="xs"
                  variant="subtle"
                  color="red"
                  disabled={busy}
                  aria-label={`Delete ${p.name}`}
                  onClick={() => void remove(p.id)}
                >
                  Delete
                </Button>
              )}
            </Group>
          </Group>
        </Paper>
      ))}
      <Group>
        <Button
          onClick={() => {
            setError(null);
            setEditing({ mode: 'create' });
          }}
        >
          New profile
        </Button>
      </Group>
    </Stack>
  );
}

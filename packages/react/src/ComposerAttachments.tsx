/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Extra context a user attaches to an outgoing message (e.g. a plan/design
 * document, or an arbitrary text snippet) — appended as a fenced block when
 * the message is sent, and shown as removable chips in the composer.
 *
 * Generic: no assumptions about what "plan"/"design" mean beyond "named
 * content the host can list". A host wires `PlanDesignListItemForAttachment[]`
 * lists from its own data (plans/designs/whatever it has) into
 * `AddAttachmentModal`; this module never fetches anything itself.
 */

import type { Dispatch, SetStateAction } from 'react';
import { useCallback, useMemo, useState } from 'react';
import {
  ActionIcon,
  Button,
  Tabs,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';

/** Extra context appended to outgoing user messages (plans, designs, or pasted text). */
export type ComposerAttachment =
  | { type: 'plan'; name: string; content: string }
  | { type: 'text'; name: string; content: string };

export type PlanDesignListItemForAttachment = {
  name: string;
  path: string;
  label?: string | null;
};

export function composerAttachmentSignature(a: ComposerAttachment): string {
  return `${a.type}:${a.name}:${a.content.length}`;
}

/** Attachment kind llmd stores composer attachments under. */
export const COMPOSER_ATTACHMENT_KIND = 'composer_attachment';

/**
 * A composer attachment as a real message attachment (structurally the
 * driver's `OutgoingAttachment`): `content` is exactly the fenced block that
 * used to be appended to the message text, so the model sees the same thing;
 * `payloadJson` keeps the structured {type, name} for display.
 */
export type ComposerOutgoingAttachment = {
  id: string;
  kind: string;
  payloadJson: string;
  content: string;
};

function composerAttachmentBlock(a: ComposerAttachment): string {
  const label = a.type === 'plan' ? `Plan: ${a.name}` : a.name;
  return `\`\`\`${label}\n${a.content}\n\`\`\``;
}

/** Converts composer attachments into message attachments for `driver.send(text, behavior, attachments)`. */
export function composerAttachmentsToMessageAttachments(
  attachments: ComposerAttachment[]
): ComposerOutgoingAttachment[] {
  return attachments.map((a) => ({
    id: `composer-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`,
    kind: COMPOSER_ATTACHMENT_KIND,
    payloadJson: JSON.stringify({ type: a.type, name: a.name }),
    content: composerAttachmentBlock(a),
  }));
}

/**
 * @deprecated Inlines attachments into the message text. Send them as real
 * attachments instead: `composerAttachmentsToMessageAttachments` (or the
 * session hook's `outgoingAttachments`).
 */
export function appendComposerAttachmentsToMessage(
  text: string,
  attachments: ComposerAttachment[]
): string {
  if (attachments.length === 0) return text;
  let out = text;
  for (const a of attachments) {
    const label = a.type === 'plan' ? `Plan: ${a.name}` : a.name;
    out += `\n\n\`\`\`${label}\n${a.content}\n\`\`\`\n`;
  }
  return out;
}

export function AddAttachmentModal({
  plansList,
  designsList,
  onClose,
  onAddPlan,
  onAddDesign,
  onAddNew,
  newAttachmentName,
  setNewAttachmentName,
  newAttachmentContent,
  setNewAttachmentContent,
  attachmentAddingId,
}: {
  plansList: PlanDesignListItemForAttachment[];
  designsList: PlanDesignListItemForAttachment[];
  onClose: () => void;
  onAddPlan: (plan: PlanDesignListItemForAttachment) => Promise<void>;
  onAddDesign: (design: PlanDesignListItemForAttachment) => Promise<void>;
  onAddNew: (name: string, content: string) => void;
  newAttachmentName: string;
  setNewAttachmentName: (v: string) => void;
  newAttachmentContent: string;
  setNewAttachmentContent: (v: string) => void;
  attachmentAddingId: string | null;
}) {
  return (
    <div
      className="context-modal-overlay add-attachment-modal-overlay"
      role="dialog"
      aria-modal="true"
      aria-labelledby="add-attachment-modal-title"
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="context-modal add-attachment-modal">
        <div className="context-modal__header">
          <h2 id="add-attachment-modal-title">Add attachment</h2>
          <ActionIcon
            type="button"
            variant="subtle"
            size="sm"
            onClick={onClose}
            aria-label="Close"
          >
            ×
          </ActionIcon>
        </div>
        <div className="context-modal__body add-attachment-modal__body">
          <Tabs defaultValue="plans" className="add-attachment-modal__tabs">
            <Tabs.List>
              <Tabs.Tab value="plans">Plans</Tabs.Tab>
              <Tabs.Tab value="designs">Designs</Tabs.Tab>
              <Tabs.Tab value="new">New attachment</Tabs.Tab>
            </Tabs.List>
            <Tabs.Panel value="plans" className="add-attachment-modal__panel">
              <div className="add-attachment-modal__list">
                {plansList.length === 0 ? (
                  <Text size="sm" c="dimmed" p="md">
                    No plans yet.
                  </Text>
                ) : (
                  <ul className="add-attachment-modal__ul">
                    {plansList.map((p) => {
                      const id = `plan:${p.name}`;
                      const loading = attachmentAddingId === id;
                      return (
                        <li key={p.name}>
                          <button
                            type="button"
                            className="add-attachment-modal__item"
                            onClick={() => onAddPlan(p)}
                            disabled={!!attachmentAddingId}
                          >
                            <Text
                              size="sm"
                              truncate
                              style={{ flex: 1, minWidth: 0 }}
                            >
                              {p.label ?? p.name}
                            </Text>
                            {loading && (
                              <Text size="xs" c="dimmed">
                                Adding…
                              </Text>
                            )}
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </div>
            </Tabs.Panel>
            <Tabs.Panel value="designs" className="add-attachment-modal__panel">
              <div className="add-attachment-modal__list">
                {designsList.length === 0 ? (
                  <Text size="sm" c="dimmed" p="md">
                    No designs yet.
                  </Text>
                ) : (
                  <ul className="add-attachment-modal__ul">
                    {designsList.map((d) => {
                      const id = `design:${d.name}`;
                      const loading = attachmentAddingId === id;
                      return (
                        <li key={d.name}>
                          <button
                            type="button"
                            className="add-attachment-modal__item"
                            onClick={() => onAddDesign(d)}
                            disabled={!!attachmentAddingId}
                          >
                            <Text
                              size="sm"
                              truncate
                              style={{ flex: 1, minWidth: 0 }}
                            >
                              {d.label ?? d.name}
                            </Text>
                            {loading && (
                              <Text size="xs" c="dimmed">
                                Adding…
                              </Text>
                            )}
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </div>
            </Tabs.Panel>
            <Tabs.Panel value="new" className="add-attachment-modal__panel">
              <div className="add-attachment-modal__form">
                <div className="add-attachment-modal__field">
                  <label htmlFor="attachment-name">Name</label>
                  <TextInput
                    id="attachment-name"
                    placeholder="e.g. Example response"
                    value={newAttachmentName}
                    onChange={(e) =>
                      setNewAttachmentName(e.currentTarget.value)
                    }
                    size="sm"
                  />
                </div>
                <div className="add-attachment-modal__field add-attachment-modal__content-wrap">
                  <label htmlFor="attachment-content">Content</label>
                  <Textarea
                    id="attachment-content"
                    value={newAttachmentContent}
                    onChange={(e) =>
                      setNewAttachmentContent(e.currentTarget.value)
                    }
                    placeholder="Paste or type content to attach. Included with your messages."
                    minRows={6}
                    maxRows={12}
                    autosize
                    styles={{ input: { fontFamily: 'inherit' } }}
                    className="add-attachment-modal__textarea"
                  />
                </div>
                <div className="add-attachment-modal__actions">
                  <Button type="button" variant="subtle" onClick={onClose}>
                    Cancel
                  </Button>
                  <Button
                    type="button"
                    disabled={!newAttachmentName.trim()}
                    onClick={() => {
                      const name = newAttachmentName.trim();
                      if (!name) return;
                      onAddNew(name, newAttachmentContent);
                    }}
                  >
                    Add
                  </Button>
                </div>
              </div>
            </Tabs.Panel>
          </Tabs>
        </div>
      </div>
    </div>
  );
}

export type ComposerAttachmentUnifiedPanelProps = {
  composerAttachments: Array<{ key: string; label: string; index: number }>;
  onComposerAttachmentAdd: () => void;
  onComposerAttachmentRemove: (index: number) => void;
  composerAttachmentAddDisabled: boolean;
};

/**
 * Session-scoped composer attachments (badges + modal) for any surface that uses `sessionId`.
 */
export function useComposerAttachmentsSessionState(
  sessionId: string | null
): {
  attachmentsBySession: Record<string, ComposerAttachment[]>;
  setAttachmentsBySession: Dispatch<
    SetStateAction<Record<string, ComposerAttachment[]>>
  >;
  attachments: ComposerAttachment[];
  setAttachments: (
    update:
      | ComposerAttachment[]
      | ((prev: ComposerAttachment[]) => ComposerAttachment[])
  ) => void;
  addAttachmentModalOpen: boolean;
  setAddAttachmentModalOpen: (v: boolean) => void;
  newAttachmentName: string;
  setNewAttachmentName: (v: string) => void;
  newAttachmentContent: string;
  setNewAttachmentContent: (v: string) => void;
  attachmentAddingId: string | null;
  setAttachmentAddingId: (v: string | null) => void;
  /** @deprecated Inlines attachments into the text; pass `outgoingAttachments` to `driver.send` instead. */
  transformContent: (text: string) => string;
  /**
   * The not-yet-sent attachments as real message attachments: pass to
   * `driver.send(text, behavior, outgoingAttachments)`, then call
   * `markComposerAttachmentsSent()`.
   */
  outgoingAttachments: ComposerOutgoingAttachment[];
  /** Call after a message is sent so attachments are not re-sent on follow-up turns. */
  markComposerAttachmentsSent: () => void;
  unifiedComposerAttachmentProps: ComposerAttachmentUnifiedPanelProps;
} {
  const [attachmentsBySession, setAttachmentsBySession] = useState<
    Record<string, ComposerAttachment[]>
  >({});
  const [sentAttachmentSigsBySession, setSentAttachmentSigsBySession] =
    useState<Record<string, string[]>>({});
  const [addAttachmentModalOpen, setAddAttachmentModalOpen] = useState(false);
  const [newAttachmentName, setNewAttachmentName] = useState('');
  const [newAttachmentContent, setNewAttachmentContent] = useState('');
  const [attachmentAddingId, setAttachmentAddingId] = useState<string | null>(
    null
  );

  const attachments = sessionId
    ? (attachmentsBySession[sessionId] ?? [])
    : [];

  const sentAttachmentSigs = useMemo(() => {
    if (!sessionId) return new Set<string>();
    return new Set(sentAttachmentSigsBySession[sessionId] ?? []);
  }, [sentAttachmentSigsBySession, sessionId]);

  const unsentAttachments = useMemo(
    () =>
      attachments.filter(
        (a) => !sentAttachmentSigs.has(composerAttachmentSignature(a))
      ),
    [attachments, sentAttachmentSigs]
  );

  const setAttachments = useCallback(
    (
      update:
        | ComposerAttachment[]
        | ((prev: ComposerAttachment[]) => ComposerAttachment[])
    ) => {
      if (!sessionId) return;
      setAttachmentsBySession((prev) => {
        const current = prev[sessionId] ?? [];
        const next = typeof update === 'function' ? update(current) : update;
        return { ...prev, [sessionId]: next };
      });
    },
    [sessionId]
  );

  const transformContent = useCallback(
    (text: string) =>
      appendComposerAttachmentsToMessage(text, unsentAttachments),
    [unsentAttachments]
  );

  const outgoingAttachments = useMemo(
    () => composerAttachmentsToMessageAttachments(unsentAttachments),
    [unsentAttachments]
  );

  const markComposerAttachmentsSent = useCallback(() => {
    if (!sessionId || unsentAttachments.length === 0) return;
    setSentAttachmentSigsBySession((prev) => {
      const existing = new Set(prev[sessionId] ?? []);
      for (const a of unsentAttachments) {
        existing.add(composerAttachmentSignature(a));
      }
      return { ...prev, [sessionId]: [...existing] };
    });
  }, [sessionId, unsentAttachments]);

  const unifiedComposerAttachmentProps: ComposerAttachmentUnifiedPanelProps =
    useMemo(
      () => ({
        composerAttachments: attachments.map((a, i) => ({
          key: `${a.type}-${a.name}-${i}`,
          label: a.type === 'plan' ? `Plan: ${a.name}` : a.name,
          index: i,
        })),
        onComposerAttachmentAdd: () => {
          setNewAttachmentName('');
          setNewAttachmentContent('');
          setAddAttachmentModalOpen(true);
        },
        onComposerAttachmentRemove: (index: number) =>
          setAttachments((prev) => prev.filter((_, idx) => idx !== index)),
        composerAttachmentAddDisabled:
          !sessionId || attachmentAddingId != null,
      }),
      [attachments, sessionId, attachmentAddingId, setAttachments]
    );

  return {
    attachmentsBySession,
    setAttachmentsBySession,
    attachments,
    setAttachments,
    addAttachmentModalOpen,
    setAddAttachmentModalOpen,
    newAttachmentName,
    setNewAttachmentName,
    newAttachmentContent,
    setNewAttachmentContent,
    attachmentAddingId,
    setAttachmentAddingId,
    transformContent,
    outgoingAttachments,
    markComposerAttachmentsSent,
    unifiedComposerAttachmentProps,
  };
}

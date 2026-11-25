/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * UI view of a tracked-document version attached to a session (badges + viewer).
 * These are display-only: the model-facing injection is assembled server-side in
 * llmd and never stored in message content.
 */
export type TrackedDocumentRefView = {
  documentId: string;
  name: string;
  /** 0-based version index for this document within the session. */
  version: number;
  /** The user message this version was attached to (badge anchor). */
  anchorMessageId: string;
  change: 'initial' | 'updated';
  /** Full snapshot content (front matter included) for the viewer. */
  content: string;
};

/** Session-scoped tracked context surfaced to the UI. */
export type SessionTrackedContext = {
  /** First-turn context seed (graph status, etc.); empty when none. */
  initialSeed: string;
  /** All tracked-document versions attached across the session, oldest first. */
  documents: TrackedDocumentRefView[];
};

export const EMPTY_SESSION_TRACKED_CONTEXT: SessionTrackedContext = {
  initialSeed: '',
  documents: [],
};

/** Map raw llmd TrackedDocumentRef wire rows (+ seed) to the UI view shape. */
export function trackedContextFromRows(
  rows:
    | ReadonlyArray<{
        documentId?: string;
        name?: string;
        version?: number;
        anchorMessageId?: string;
        change?: string;
        content?: string;
      }>
    | undefined,
  initialSeed: string | undefined
): SessionTrackedContext {
  return {
    initialSeed: initialSeed ?? '',
    documents: (rows ?? []).map((r) => ({
      documentId: r.documentId ?? '',
      name: r.name ?? '',
      version: r.version ?? 0,
      anchorMessageId: r.anchorMessageId ?? '',
      change: r.change === 'updated' ? 'updated' : 'initial',
      content: r.content ?? '',
    })),
  };
}

/** Group tracked-document refs by the user message they were attached to. */
export function trackedDocumentsByMessage(
  documents: readonly TrackedDocumentRefView[]
): Map<string, TrackedDocumentRefView[]> {
  const byMsg = new Map<string, TrackedDocumentRefView[]>();
  for (const doc of documents) {
    const key = doc.anchorMessageId.trim();
    if (!key) continue;
    const list = byMsg.get(key);
    if (list) list.push(doc);
    else byMsg.set(key, [doc]);
  }
  return byMsg;
}

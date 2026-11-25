/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * The composer draft as a subscribable store rather than React state.
 *
 * The chat driver is created by the HOST (the page that renders the chat), so
 * if the draft were `useState` inside the driver every keystroke would
 * re-render that whole page. Keeping it in a store means only the components
 * that read it with {@link useDraft} re-render while typing.
 */

import { useSyncExternalStore } from 'react';

/** Read side of the composer draft, as exposed on `LlmdChatDriver.draft`. */
export interface DraftStore {
  get(): string;
  subscribe(listener: () => void): () => void;
}

/** Internal: the driver's writable handle. */
export interface WritableDraftStore extends DraftStore {
  set(next: string | ((current: string) => string)): void;
}

export function createDraftStore(): WritableDraftStore {
  let value = '';
  const listeners = new Set<() => void>();
  return {
    get: () => value,
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    set(next) {
      const resolved = typeof next === 'function' ? next(value) : next;
      if (resolved === value) return;
      value = resolved;
      for (const l of [...listeners]) l();
    },
  };
}

/** Current draft text; re-renders only the calling component on change. */
export function useDraft(store: DraftStore): string {
  return useSyncExternalStore(store.subscribe, store.get, store.get);
}

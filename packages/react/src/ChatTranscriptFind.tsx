/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { memo, useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import { ChatTranscriptFindBar } from './ChatTranscriptFindBar.js';
import {
  applyChatTranscriptFindHighlights,
  clearChatTranscriptFindHighlights,
  collectChatTranscriptMatchRanges,
  scrollChatTranscriptMatchIntoView,
} from './chatTranscriptFindHighlights.js';
import { isModKey } from './keyboardShortcuts.js';

const FIND_DEBOUNCE_MS = 500;
/** Highlight API slows down with huge match lists; cap painted highlights only. */
const MAX_PAINTED_MATCHES = 300;

export type ChatTranscriptFindProps = {
  rootRef: RefObject<HTMLElement | null>;
  scrollViewportRef: RefObject<HTMLElement | null>;
  /** Changing this (e.g. on new streamed content) re-runs the active search against the updated DOM. */
  contentKey: string;
  /** Closes and resets find state when this changes (e.g. switching sessions). */
  sessionId: string | null;
  placeholder?: string;
  ariaLabel?: string;
};

/**
 * Cmd/Ctrl+F find-in-transcript: text search + highlight over a DOM subtree
 * (via the CSS Custom Highlight API when available), with Cmd/Ctrl+G
 * next/previous and Escape to close. Host mounts this once inside the chat
 * surface and gives it refs to the searchable root and scroll viewport.
 */
export const ChatTranscriptFind = memo(function ChatTranscriptFind({
  rootRef,
  scrollViewportRef,
  contentKey,
  sessionId,
  placeholder,
  ariaLabel,
}: ChatTranscriptFindProps) {
  const [open, setOpen] = useState(false);
  const [inputValue, setInputValue] = useState('');
  const [matchLabel, setMatchLabel] = useState('');

  const rangesRef = useRef<Range[]>([]);
  const currentIndexRef = useRef(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const appliedQueryRef = useRef('');
  const inputValueRef = useRef(inputValue);
  const prevSessionIdRef = useRef(sessionId);
  inputValueRef.current = inputValue;

  const revealIndex = useCallback(
    (index: number) => {
      const ranges = rangesRef.current;
      if (index < 0 || index >= ranges.length) return;
      applyChatTranscriptFindHighlights(ranges, index, MAX_PAINTED_MATCHES);
      const viewport = scrollViewportRef.current;
      const range = ranges[index];
      if (viewport && range) {
        scrollChatTranscriptMatchIntoView(range, viewport);
      }
    },
    [scrollViewportRef]
  );

  const updateMatchLabel = useCallback((index: number, count: number) => {
    setMatchLabel(count === 0 ? 'No matches' : `${index + 1}/${count}`);
  }, []);

  const applySearch = useCallback(
    (searchQuery: string, preserveIndex: boolean, scrollToMatch: boolean) => {
      const root = rootRef.current;
      const trimmed = searchQuery.trim();
      appliedQueryRef.current = trimmed;

      if (!root || !trimmed) {
        rangesRef.current = [];
        currentIndexRef.current = 0;
        clearChatTranscriptFindHighlights();
        setMatchLabel('');
        return;
      }

      const ranges = collectChatTranscriptMatchRanges(root, trimmed);
      rangesRef.current = ranges;
      const count = ranges.length;

      if (count === 0) {
        currentIndexRef.current = 0;
        clearChatTranscriptFindHighlights();
        setMatchLabel('No matches');
        return;
      }

      const index = preserveIndex
        ? Math.min(currentIndexRef.current, count - 1)
        : 0;
      currentIndexRef.current = index;
      updateMatchLabel(index, count);

      if (scrollToMatch) {
        revealIndex(index);
      } else {
        applyChatTranscriptFindHighlights(ranges, index, MAX_PAINTED_MATCHES);
      }
    },
    [rootRef, revealIndex, updateMatchLabel]
  );

  const cancelPendingSearch = useCallback(() => {
    if (debounceTimerRef.current != null) {
      clearTimeout(debounceTimerRef.current);
      debounceTimerRef.current = null;
    }
  }, []);

  const flushSearch = useCallback(
    (preserveIndex: boolean, scrollToMatch: boolean) => {
      cancelPendingSearch();
      applySearch(inputValueRef.current, preserveIndex, scrollToMatch);
    },
    [applySearch, cancelPendingSearch]
  );

  const scheduleSearch = useCallback(
    (searchQuery: string, preserveIndex: boolean, scrollToMatch: boolean) => {
      cancelPendingSearch();
      const trimmed = searchQuery.trim();
      if (!trimmed) {
        applySearch('', preserveIndex, scrollToMatch);
        return;
      }

      debounceTimerRef.current = setTimeout(() => {
        debounceTimerRef.current = null;
        applySearch(searchQuery, preserveIndex, scrollToMatch);
      }, FIND_DEBOUNCE_MS);
    },
    [applySearch, cancelPendingSearch]
  );

  const closeFind = useCallback(() => {
    cancelPendingSearch();
    setOpen(false);
    setInputValue('');
    setMatchLabel('');
    appliedQueryRef.current = '';
    currentIndexRef.current = 0;
    rangesRef.current = [];
    clearChatTranscriptFindHighlights();
  }, [cancelPendingSearch]);

  const openFind = useCallback((seedQuery?: string) => {
    setOpen(true);
    if (seedQuery != null) {
      setInputValue(seedQuery);
      appliedQueryRef.current = '';
      currentIndexRef.current = 0;
      cancelPendingSearch();
      applySearch(seedQuery, false, true);
    }
    requestAnimationFrame(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    });
  }, [applySearch, cancelPendingSearch]);

  const findNext = useCallback(() => {
    if (debounceTimerRef.current != null) {
      flushSearch(false, true);
      return;
    }
    const count = rangesRef.current.length;
    if (count === 0) return;
    const next = (currentIndexRef.current + 1) % count;
    currentIndexRef.current = next;
    updateMatchLabel(next, count);
    revealIndex(next);
  }, [flushSearch, revealIndex, updateMatchLabel]);

  const findPrevious = useCallback(() => {
    if (debounceTimerRef.current != null) {
      flushSearch(false, true);
      return;
    }
    const count = rangesRef.current.length;
    if (count === 0) return;
    const next = (currentIndexRef.current - 1 + count) % count;
    currentIndexRef.current = next;
    updateMatchLabel(next, count);
    revealIndex(next);
  }, [flushSearch, revealIndex, updateMatchLabel]);

  const handleInputChange = useCallback(
    (value: string) => {
      setInputValue(value);
      currentIndexRef.current = 0;
      scheduleSearch(value, false, true);
    },
    [scheduleSearch]
  );

  useEffect(() => {
    if (!open) return;
    if (debounceTimerRef.current != null) return;
    const q = inputValueRef.current.trim();
    if (!q) return;
    applySearch(q, true, false);
  }, [contentKey, open, applySearch]);

  useEffect(() => {
    if (prevSessionIdRef.current === sessionId) return;
    prevSessionIdRef.current = sessionId;
    closeFind();
  }, [sessionId, closeFind]);

  useEffect(() => {
    if (!open) clearChatTranscriptFindHighlights();
    return () => {
      cancelPendingSearch();
      clearChatTranscriptFindHighlights();
    };
  }, [open, cancelPendingSearch]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const mod = isModKey(e);
      const key = e.key.toLowerCase();

      if (mod && key === 'f') {
        e.preventDefault();
        const selected = window.getSelection()?.toString().trim();
        const seed =
          selected && !selected.includes('\n') && selected.length <= 500
            ? selected
            : undefined;
        if (open && seed == null) {
          inputRef.current?.focus();
          inputRef.current?.select();
        } else {
          openFind(seed ?? (open ? inputValueRef.current : undefined));
        }
        return;
      }

      if (!open) return;

      if (mod && key === 'g') {
        e.preventDefault();
        if (e.shiftKey) findPrevious();
        else findNext();
        return;
      }

      if (key === 'escape') {
        e.preventDefault();
        closeFind();
      }
    };

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open, openFind, closeFind, findNext, findPrevious]);

  if (!open) return null;

  const trimmedInput = inputValue.trim();
  const displayMatchLabel =
    trimmedInput.length === 0
      ? ''
      : trimmedInput !== appliedQueryRef.current
        ? '…'
        : matchLabel;

  return (
    <ChatTranscriptFindBar
      query={inputValue}
      onQueryChange={handleInputChange}
      inputRef={inputRef}
      matchLabel={displayMatchLabel}
      onFindNext={findNext}
      onFindPrevious={findPrevious}
      onClose={closeFind}
      placeholder={placeholder}
      ariaLabel={ariaLabel}
    />
  );
});

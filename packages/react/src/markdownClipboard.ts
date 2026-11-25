/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * Copy behavior for markdown-rendered surfaces:
 * - Cmd/Ctrl+C: raw markdown (from source or HTML→markdown via Turndown)
 * - Shift+Cmd/Ctrl+C: plain text only (visible selection, no markdown syntax)
 */

import TurndownService from 'turndown';

/** Modifier keys are not on ClipboardEvent in lib.dom but are set by browsers on copy. */
export function copyEventShiftKey(e: ClipboardEvent): boolean {
  return Boolean((e as ClipboardEvent & { shiftKey?: boolean }).shiftKey);
}

let turndownSingleton: TurndownService | null = null;

function getTurndown(): TurndownService {
  if (!turndownSingleton) {
    turndownSingleton = new TurndownService({
      headingStyle: 'atx',
      codeBlockStyle: 'fenced',
      emDelimiter: '*',
    });
    turndownSingleton.addRule('fencedCode', {
      filter: (node: HTMLElement) =>
        node.nodeName === 'PRE' &&
        node.firstChild != null &&
        node.firstChild.nodeName === 'CODE',
      replacement: (_content: string, node: HTMLElement) => {
        const codeEl = node.firstChild as HTMLElement;
        const langMatch = /language-(\S+)/.exec(codeEl.className || '');
        const lang = langMatch?.[1] ?? '';
        const code = codeEl.textContent ?? '';
        return `\n\`\`\`${lang}\n${code}\n\`\`\`\n`;
      },
    });
  }
  return turndownSingleton;
}

/** Plain text for the current DOM selection, or the root's visible text when empty. */
export function plainTextFromSelection(root: HTMLElement | null): string {
  const sel = window.getSelection();
  if (sel && sel.rangeCount > 0 && !sel.isCollapsed) {
    return sel.toString();
  }
  return root?.innerText ?? '';
}

/** Convert a cloned selection fragment to markdown (approximate). */
export function htmlFragmentToMarkdown(fragment: DocumentFragment): string {
  const host = document.createElement('div');
  host.appendChild(fragment);
  return getTurndown().turndown(host.innerHTML).trim();
}

export type MarkdownCopyHandlerOptions = {
  /** Full document markdown when the selection cannot be mapped (optional). */
  sourceMarkdown?: string;
  /** Root element used for plain-text fallback and selection containment checks. */
  root: HTMLElement;
  /** When set, used for markdown copy instead of Turndown (e.g. MDXEditor API). */
  getMarkdownForCopy?: () => string;
};

export function handleMarkdownRenderedCopy(
  e: ClipboardEvent,
  opts: MarkdownCopyHandlerOptions
): void {
  const { root, sourceMarkdown, getMarkdownForCopy } = opts;
  const sel = window.getSelection();
  if (!sel || sel.rangeCount === 0) return;
  if (!root.contains(sel.anchorNode) && !root.contains(sel.focusNode)) return;

  const plain = plainTextFromSelection(root);

  if (copyEventShiftKey(e)) {
    if (!plain.trim()) return;
    e.preventDefault();
    e.stopPropagation();
    e.clipboardData?.setData('text/plain', plain);
    return;
  }

  let md = '';
  if (getMarkdownForCopy) {
    md = getMarkdownForCopy();
  } else if (!sel.isCollapsed) {
    const range = sel.getRangeAt(0);
    md = htmlFragmentToMarkdown(range.cloneContents());
  }
  if (!md.trim() && sourceMarkdown?.trim()) {
    md = sourceMarkdown;
  }
  if (!md.trim()) return;

  e.preventDefault();
  e.stopPropagation();
  e.clipboardData?.setData('text/plain', md);
  e.clipboardData?.setData('text/markdown', md);
}

/**
 * Clipboard copy handler for markdown-rendered UI.
 * Attach with addEventListener('copy', handler, true) on the render root.
 * Re-bind (e.g. from a useEffect) when `sourceMarkdown` changes.
 */
export function bindMarkdownRenderedCopy(
  root: HTMLElement | null,
  sourceMarkdown?: string,
  getMarkdownForCopy?: () => string
): (() => void) | undefined {
  if (!root) return undefined;
  const onCopy = (e: ClipboardEvent) => {
    handleMarkdownRenderedCopy(e, { root, sourceMarkdown, getMarkdownForCopy });
  };
  root.addEventListener('copy', onCopy, true);
  return () => root.removeEventListener('copy', onCopy, true);
}

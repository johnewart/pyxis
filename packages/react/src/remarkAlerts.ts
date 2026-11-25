/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/** Remark plugin: GitHub alert blockquotes (`> [!NOTE]`) become styled callouts. */

const KINDS = new Set(['note', 'tip', 'important', 'warning', 'caution']);
const MARKER = /^\s*\[!([A-Za-z]+)\][ \t]*(?:\r?\n)?/;

type MdNode = {
  type: string;
  value?: string;
  children?: MdNode[];
  data?: { hName?: string; hProperties?: Record<string, unknown> };
};

function applyAlert(quote: MdNode): void {
  const para = quote.children?.[0];
  if (!para || para.type !== 'paragraph' || !para.children?.length) return;
  const first = para.children[0];
  if (first.type !== 'text' || typeof first.value !== 'string') return;
  const m = MARKER.exec(first.value);
  if (!m) return;
  const kind = m[1].toLowerCase();
  if (!KINDS.has(kind)) return;

  const rest = first.value.slice(m[0].length);
  if (rest) {
    first.value = rest;
  } else {
    para.children.shift();
    if (para.children[0]?.type === 'break') para.children.shift();
  }
  if (para.children.length === 0) quote.children!.shift();

  quote.children!.unshift({
    type: 'paragraph',
    data: { hName: 'div', hProperties: { className: ['llmd-callout__title'] } },
    children: [{ type: 'text', value: kind.charAt(0).toUpperCase() + kind.slice(1) }],
  });

  quote.data = {
    ...quote.data,
    hName: 'blockquote',
    hProperties: { className: ['llmd-callout', `llmd-callout--${kind}`] },
  };
}

function walk(node: MdNode): void {
  if (node.type === 'blockquote') applyAlert(node);
  node.children?.forEach(walk);
}

export function remarkAlerts() {
  return (tree: MdNode) => {
    walk(tree);
  };
}

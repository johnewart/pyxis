/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { createContext, useContext, useMemo } from 'react';
import type { Options as ReactMarkdownOptions } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { remarkAlerts } from './remarkAlerts.js';

export type MarkdownPluginList = NonNullable<ReactMarkdownOptions['remarkPlugins']>;

/**
 * Extra unified plugins for every @pyxis-llm/react markdown surface (chat messages,
 * paste messages, streamed text). They are APPENDED to the built-in
 * `remark-gfm`, never replace it.
 *
 * - Pass stable (module-level or memoized) arrays: a fresh array per render
 *   invalidates every memoized markdown block and forces a re-parse.
 * - Streamed text is parsed block-by-block (split on top-level blank lines),
 *   so plugins whose syntax spans blank lines (e.g. multi-paragraph
 *   directives) only see one block at a time while streaming.
 * - Adding `rehype-raw` (or similar) renders raw HTML from model output; that
 *   is an explicit opt-out of sanitization — pair it with `rehype-sanitize`.
 */
export type MarkdownPlugins = {
  remarkPlugins?: MarkdownPluginList;
  rehypePlugins?: MarkdownPluginList;
};

const MarkdownPluginsContext = createContext<MarkdownPlugins>({});

/**
 * Register markdown plugins for every markdown surface below. Nested providers
 * append to their parent's plugins (parent first).
 */
export function MarkdownPluginProvider({
  plugins,
  children,
}: {
  plugins: MarkdownPlugins;
  children?: React.ReactNode;
}) {
  const parent = useContext(MarkdownPluginsContext);
  const merged = useMemo<MarkdownPlugins>(
    () => ({
      remarkPlugins: [...(parent.remarkPlugins ?? []), ...(plugins.remarkPlugins ?? [])],
      rehypePlugins: [...(parent.rehypePlugins ?? []), ...(plugins.rehypePlugins ?? [])],
    }),
    [parent, plugins.remarkPlugins, plugins.rehypePlugins]
  );
  return (
    <MarkdownPluginsContext.Provider value={merged}>{children}</MarkdownPluginsContext.Provider>
  );
}

const BUILTIN_REMARK: MarkdownPluginList = [remarkGfm, remarkAlerts];

/** Resolved plugin lists (built-ins + registered) for a `<ReactMarkdown>`. Stable across renders. */
export function useMarkdownPlugins(): Required<MarkdownPlugins> {
  const ctx = useContext(MarkdownPluginsContext);
  return useMemo(
    () => ({
      remarkPlugins: ctx.remarkPlugins?.length
        ? [...BUILTIN_REMARK, ...ctx.remarkPlugins]
        : BUILTIN_REMARK,
      rehypePlugins: ctx.rehypePlugins ?? [],
    }),
    [ctx]
  );
}

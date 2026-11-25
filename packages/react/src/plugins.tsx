/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * llmd's one extension point for what the chat can render. A plugin may
 * contribute any of:
 *
 *   - `highlighter`: syntax highlighting for fenced code (`@pyxis-llm/react/highlight`);
 *   - `languages`: a renderer for a whole fence language (```mermaid, ```vega-lite);
 *   - `displayCards`: a renderer for a display card type (@pyxis-llm/core
 *     DISPLAY_CARD_KIND, e.g. "chart" from `@pyxis-llm/react/charts`);
 *   - `attachments`: a renderer (and transcript placement) for a host's own
 *     attachment kind (e.g. a command-proposal card).
 *
 * Plugins reach the chat two ways, and both merge:
 *
 *   - `registerLlmdPlugin(plugin)`: global, for the whole page. Importing
 *     `@pyxis-llm/react/charts` does this itself, so "import it" is all a host
 *     needs to do to get charts.
 *   - `<LlmdPluginProvider plugins={[...]}>`: scoped to a subtree; nested
 *     providers stack. Use it for plugins that depend on where they render.
 *
 * Later registrations win (a provider beats the global registry; the last
 * plugin in a list beats earlier ones), so a host can override anything llmd
 * ships, including the built-in image card.
 *
 * Renderers are components, not callbacks: one that needs host state (e.g. a
 * "pin this" action) reads it from the host's own React context.
 */

import React, { createContext, useContext, useMemo, useSyncExternalStore } from 'react';
import type { AttachmentKindPlugin, DisplayCard, LlmdMessageAttachment } from '@pyxis-llm/core';
// Type-only imports in the other direction keep this free of a runtime cycle.
import { imageCardPlugin } from './ImageDisplayCard.js';

/** Props handed to a syntax highlighter for a fenced code block. */
export type HighlightedCodeProps = {
  code: string;
  language: string;
  /** Box styling for the block (margins/padding/wrapping), chosen by the markdown surface. */
  customStyle: React.CSSProperties;
  /** Must be spread onto the outermost element the highlighter renders. */
  preTagProps: Record<string, unknown>;
};

/** Renders a whole fenced block for one language (e.g. ```mermaid). */
export type LanguageBlockRenderer = React.ComponentType<{ code: string }>;

/** Props a display card body renderer receives. */
export type DisplayCardRenderProps<Body = Record<string, unknown>> = {
  /** The card, with `body` already decoded (see DisplayCardRenderer.decode). */
  card: DisplayCard<Body>;
};

/**
 * Renders one display card type. Unless `ownsChrome`, the card is drawn in
 * llmd's shared frame (title above, caption below); the renderer draws only
 * the body.
 */
export type DisplayCardRenderer<Body = Record<string, unknown>> = {
  /** Validate / normalize the body; undefined shows "[<type> unavailable]". Default: accept as-is. */
  decode?: (body: Record<string, unknown>) => Body | undefined;
  render: React.ComponentType<DisplayCardRenderProps<Body>>;
  /** The renderer draws the title and caption itself. */
  ownsChrome?: boolean;
};

/** What a host attachment renderer receives. */
export type AttachmentRenderProps = {
  attachment: LlmdMessageAttachment;
  /** The id in the `<attachment:id/>` tag that placed it. */
  attachmentId: string;
  /** Id of the message the card renders in. */
  messageId?: string;
  /** Remove the card from the session (wired when the panel was given a dismiss handler). */
  dismiss?: () => void;
};

/** A host attachment kind: how it renders, and (optionally) where it is placed. */
export type AttachmentKindRenderer = {
  render?: React.ComponentType<AttachmentRenderProps>;
  /** Transcript placement (own row, persistence, anchors); see @pyxis-llm/core AttachmentKindPlugin. */
  placement?: Omit<AttachmentKindPlugin, 'kind'>;
};

export type LlmdPlugin = {
  /** Stable id: registering the same id again replaces the earlier plugin. */
  id: string;
  highlighter?: React.ComponentType<HighlightedCodeProps>;
  /** Keyed by fence language, lower-case. Takes precedence over `highlighter`. */
  languages?: Record<string, LanguageBlockRenderer>;
  /** Keyed by display card `type`. */
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  displayCards?: Record<string, DisplayCardRenderer<any>>;
  /** Keyed by attachment `kind`. */
  attachments?: Record<string, AttachmentKindRenderer>;
};

/** Every plugin merged into one lookup. */
export type ResolvedLlmdPlugins = {
  highlighter?: React.ComponentType<HighlightedCodeProps>;
  languages: Record<string, LanguageBlockRenderer>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  displayCards: Record<string, DisplayCardRenderer<any>>;
  attachments: Record<string, AttachmentKindRenderer>;
  /** Placement rules for @pyxis-llm/core's transcript functions. */
  attachmentKindPlugins: AttachmentKindPlugin[];
};

export function resolveLlmdPlugins(plugins: readonly LlmdPlugin[]): ResolvedLlmdPlugins {
  const out: ResolvedLlmdPlugins = { languages: {}, displayCards: {}, attachments: {}, attachmentKindPlugins: [] };
  for (const p of plugins) {
    if (p.highlighter) out.highlighter = p.highlighter;
    Object.assign(out.languages, lowerKeys(p.languages));
    Object.assign(out.displayCards, p.displayCards);
    Object.assign(out.attachments, p.attachments);
  }
  for (const [kind, r] of Object.entries(out.attachments)) {
    if (r.placement) out.attachmentKindPlugins.push({ kind, ...r.placement });
  }
  return out;
}

function lowerKeys<T>(rec: Record<string, T> | undefined): Record<string, T> {
  const out: Record<string, T> = {};
  for (const [k, v] of Object.entries(rec ?? {})) out[k.toLowerCase()] = v;
  return out;
}

/* ---- Global registry ---- */

let registered: LlmdPlugin[] = [];
const listeners = new Set<() => void>();

/**
 * Register a plugin for the whole page. Re-registering an id replaces it (so
 * hot reload and double imports are harmless). Returns an unregister function.
 */
export function registerLlmdPlugin(plugin: LlmdPlugin): () => void {
  registered = [...registered.filter((p) => p.id !== plugin.id), plugin];
  for (const l of listeners) l();
  return () => {
    if (!registered.includes(plugin)) return;
    registered = registered.filter((p) => p !== plugin);
    for (const l of listeners) l();
  };
}

/** The globally registered plugins, in registration order. */
export function registeredLlmdPlugins(): readonly LlmdPlugin[] {
  return registered;
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

/* ---- Scoped providers ---- */

const ScopedPluginsContext = createContext<readonly LlmdPlugin[]>([]);

/** Add plugins for everything below. Nested providers stack on their parent's. */
export function LlmdPluginProvider({ plugins, children }: { plugins: readonly LlmdPlugin[]; children?: React.ReactNode }) {
  const parent = useContext(ScopedPluginsContext);
  const stacked = useMemo(() => (plugins.length ? [...parent, ...plugins] : parent), [parent, plugins]);
  return <ScopedPluginsContext.Provider value={stacked}>{children}</ScopedPluginsContext.Provider>;
}

/** llmd's built-ins (the image card). Always first, so anything overrides them. */
const builtins: readonly LlmdPlugin[] = [imageCardPlugin];

/** Everything in effect here: llmd's built-ins, then global registrations, then providers. */
export function useLlmdPlugins(): ResolvedLlmdPlugins {
  const global = useSyncExternalStore(subscribe, registeredLlmdPlugins, registeredLlmdPlugins);
  const scoped = useContext(ScopedPluginsContext);
  return useMemo(() => resolveLlmdPlugins([...builtins, ...global, ...scoped]), [global, scoped]);
}

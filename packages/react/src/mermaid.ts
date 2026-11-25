/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * `@pyxis-llm/react/mermaid` — mermaid diagram rendering for ```mermaid fences.
 * Requires the optional peer `mermaid` (and optionally `@mermaid-js/layout-elk`).
 *
 *   import { mermaidPlugin } from '@pyxis-llm/react/mermaid';
 *   registerLlmdPlugin(mermaidPlugin);
 *
 * Not registered on import: a host may draw mermaid its own way (for
 * example, to match the styling of its own documents).
 */

import type { LlmdPlugin } from './plugins.js';
import { MermaidMarkdownCodeBlock } from './MermaidDiagram.js';

export type { MermaidDiagramProps } from './MermaidDiagram.js';
export { MermaidDiagram, MermaidMarkdownCodeBlock } from './MermaidDiagram.js';
export {
  normalizeMermaidSource,
  renderMermaidDiagram,
  resetMermaidRuntimeForTests,
} from './mermaidRuntime.js';
export type { RenderMermaidResult } from './mermaidRuntime.js';

export const mermaidPlugin: LlmdPlugin = {
  id: 'llmd.mermaid',
  languages: { mermaid: MermaidMarkdownCodeBlock },
};

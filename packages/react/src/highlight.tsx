/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * `@pyxis-llm/react/highlight` — Prism syntax highlighting for fenced code.
 * Requires the optional peer `react-syntax-highlighter`.
 *
 *   import { highlightPlugin } from '@pyxis-llm/react/highlight';
 *   registerLlmdPlugin(highlightPlugin);   // or <LlmdPluginProvider plugins={[highlightPlugin]}>
 *
 * Not registered on import: a host may want its own highlighter instead.
 */

import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import oneDark from 'react-syntax-highlighter/dist/esm/styles/prism/one-dark';
import type { HighlightedCodeProps, LlmdPlugin } from './plugins.js';

/**
 * Uses app CSS variables for code block background and base text, keeping
 * Prism token colors from oneDark. Consumers must define `--app-code-bg`,
 * `--app-code-text`, and `--mono-font` in their CSS.
 */
const codeBlockStyle = {
  ...oneDark,
  'code[class*="language-"]': {
    ...oneDark['code[class*="language-"]' as keyof typeof oneDark],
    background: 'var(--app-code-bg)',
    color: 'var(--app-code-text)',
    fontFamily: 'var(--mono-font)',
  },
  'pre[class*="language-"]': {
    ...oneDark['pre[class*="language-"]' as keyof typeof oneDark],
    background: 'var(--app-code-bg)',
    color: 'var(--app-code-text)',
    fontFamily: 'var(--mono-font)',
  },
};

export function PrismHighlightedCode({ code, language, customStyle, preTagProps }: HighlightedCodeProps) {
  return (
    <SyntaxHighlighter
      PreTag="div"
      PreTagProps={preTagProps}
      language={language}
      style={codeBlockStyle}
      customStyle={customStyle}
      codeTagProps={{
        style: {
          fontFamily: 'var(--mono-font)',
          whiteSpace: 'pre-wrap',
          overflowWrap: 'anywhere',
          wordBreak: 'break-word',
        },
      }}
      showLineNumbers={false}
    >
      {code}
    </SyntaxHighlighter>
  );
}

export const highlightPlugin: LlmdPlugin = { id: 'llmd.highlight', highlighter: PrismHighlightedCode };

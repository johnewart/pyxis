/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { startTransition, useEffect, useRef, useState } from 'react';
import type { Components } from 'react-markdown';
import { useLlmdPlugins } from './plugins.js';

const SYNTAX_BLOCK_ATTR = 'data-syntax-highlight-block';

/** Plain text of a React node tree: the fence's source as markdown gave it. */
function nodeText(node: React.ReactNode): string {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join('');
  if (React.isValidElement(node)) {
    return nodeText((node.props as { children?: React.ReactNode }).children);
  }
  return '';
}

const COPY_RESET_MS = 1500;
/** Failures stay up longer so they are actually read. */
const COPY_FAILED_RESET_MS = 5000;

/**
 * Puts text on the clipboard; false when the page may not.
 *
 * The async Clipboard API is refused in an iframe that was not granted
 * clipboard-write (host apps often render chat in one) and is absent outside a
 * secure context. The legacy path - select a hidden textarea and
 * execCommand('copy') - has no such restriction, but only while the click's
 * user activation is live, so it runs first and synchronously.
 */
async function copyText(text: string): Promise<boolean> {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.position = 'fixed';
  ta.style.top = '0';
  ta.style.left = '0';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch {
    ok = false;
  }
  document.body.removeChild(ta);
  if (ok) return true;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Refused as well.
  }
  return false;
}

/** 16px stroke icons (tabler shapes), inline: this package has no icon dependency. */
function CopyIcon({ state }: { state: 'idle' | 'copied' | 'failed' }) {
  const paths =
    state === 'copied' ? (
      <path d="M5 12l5 5l10 -10" />
    ) : state === 'failed' ? (
      <>
        <path d="M12 9v4" />
        <path d="M10.363 3.591l-8.106 13.534a1.914 1.914 0 0 0 1.636 2.871h16.214a1.914 1.914 0 0 0 1.636 -2.87l-8.106 -13.536a1.914 1.914 0 0 0 -3.274 0z" />
        <path d="M12 16h.01" />
      </>
    ) : (
      <>
        <path d="M7 7m0 2.667a2.667 2.667 0 0 1 2.667 -2.667h8.666a2.667 2.667 0 0 1 2.667 2.667v8.666a2.667 2.667 0 0 1 -2.667 2.667h-8.666a2.667 2.667 0 0 1 -2.667 -2.667z" />
        <path d="M4.012 16.737a2.005 2.005 0 0 1 -1.012 -1.737v-10c0 -1.1 .9 -2 2 -2h10c.75 0 1.158 .385 1.5 1" />
      </>
    );
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{ display: 'block' }}
    >
      {paths}
    </svg>
  );
}

/**
 * Copies the fence's raw source (not the rendered DOM, which carries
 * highlighter markup, escapes and soft wraps).
 */
function CopyCodeButton({ text, visible }: { text: string; visible: boolean }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  useEffect(() => {
    if (state === 'idle') return;
    const id = window.setTimeout(
      () => setState('idle'),
      state === 'failed' ? COPY_FAILED_RESET_MS : COPY_RESET_MS,
    );
    return () => clearTimeout(id);
  }, [state]);

  const onClick = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setState((await copyText(text)) ? 'copied' : 'failed');
  };

  const failed = state === 'failed';
  const label =
    state === 'copied'
      ? 'Copied'
      : failed
        ? 'Copy failed: the browser refused clipboard access'
        : 'Copy code to clipboard';
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      role={failed ? 'alert' : undefined}
      style={{
        position: 'absolute',
        // Inner blocks carry a 1em/1.25em margin; sit inside their corner.
        top: 'calc(1em + 4px)',
        right: 'calc(1.25em + 4px)',
        zIndex: 1,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 4,
        lineHeight: 0,
        // Icon only, on a transparent ground. The code-text color is the
        // theme's readable foreground: dark in light themes, light in dark.
        color: failed ? '#e03131' : 'var(--app-code-text, currentColor)',
        background: 'transparent',
        border: 'none',
        borderRadius: '4px',
        cursor: 'pointer',
        opacity: failed ? 1 : visible || state !== 'idle' ? 0.75 : 0,
        transition: 'opacity 120ms ease-in-out',
      }}
    >
      <CopyIcon state={state} />
    </button>
  );
}

function Pre({ children, ...props }: React.ComponentPropsWithoutRef<'pre'>) {
  const [hovered, setHovered] = useState(false);
  const child = React.Children.only(children) as React.ReactElement | undefined;
  const childProps = child?.props as Record<string, unknown> | undefined;
  const text = nodeText(childProps?.children as React.ReactNode).replace(/\n$/, '');
  const body = childProps?.[SYNTAX_BLOCK_ATTR] ? <>{children}</> : <pre {...props}>{children}</pre>;
  return (
    <div
      style={{ position: 'relative' }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setHovered(true)}
      onBlur={() => setHovered(false)}
    >
      {body}
      <CopyCodeButton text={text} visible={hovered} />
    </div>
  );
}

const PLAIN_FENCE_BASE: React.CSSProperties = {
  margin: '1em 1.25em 2.25em 1.25em',
  padding: '10px 12px',
  borderRadius: '6px',
  fontSize: '1.0em',
  background: 'var(--app-code-bg)',
  color: 'var(--app-code-text)',
  fontFamily: 'var(--mono-font)',
};

const PLAIN_FENCE_DEFAULT: React.CSSProperties = {
  ...PLAIN_FENCE_BASE,
  overflowX: 'auto',
};

/** Narrow thinking panel: wrap instead of horizontal scroll (SyntaxHighlighter uses PreTag="div"). */
const PLAIN_FENCE_THINKING_STREAM: React.CSSProperties = {
  ...PLAIN_FENCE_BASE,
  overflowX: 'hidden',
  maxWidth: '100%',
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
  wordBreak: 'break-word',
};

const HIGHLIGHTER_CUSTOM_BASE = {
  margin: '1em 1.25em 2.25em 1.25em',
  padding: '10px 12px',
  borderRadius: '6px',
  fontSize: '1.0em',
} as const;

const HIGHLIGHTER_CUSTOM_THINKING_STREAM: React.CSSProperties = {
  ...HIGHLIGHTER_CUSTOM_BASE,
  overflowX: 'hidden',
  maxWidth: '100%',
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
  wordBreak: 'break-word',
};

/** After the first paint, wait this long once `code` stops changing before running Prism. */
const SYNTAX_STABLE_MS = 220;

function createCodeComponent(plainFenceStyle: React.CSSProperties, highlighterCustom: React.CSSProperties) {
  return function Code({
    node: _node,
    inline,
    className,
    children,
    ...props
  }: React.ComponentPropsWithoutRef<'code'> & {
    node?: unknown;
    inline?: boolean;
  }) {
    const { highlighter: Highlighter, languages } = useLlmdPlugins();
    // [\w-]: hyphenated fence languages (```vega-lite, ```shell-session) must
    // reach the language/highlighter lookup whole, not truncated at the dash.
    const match = /language-([\w-]+)/.exec(className || '');
    const code = String(children).replace(/\n$/, '');
    const LanguageBlock =
      !inline && match ? languages?.[match[1].toLowerCase()] : undefined;

    const [syntaxReady, setSyntaxReady] = useState(true);
    const prevCodeRef = useRef(code);

    useEffect(() => {
      if (inline || !match || LanguageBlock || !Highlighter) return;
      if (prevCodeRef.current === code) return;
      prevCodeRef.current = code;
      startTransition(() => {
        setSyntaxReady(false);
      });
      const id = window.setTimeout(() => {
        setSyntaxReady(true);
      }, SYNTAX_STABLE_MS);
      return () => clearTimeout(id);
    }, [code, inline, LanguageBlock, Highlighter, match]);

    if (LanguageBlock) {
      return <LanguageBlock code={code} />;
    }

    if (!inline && match) {
      const lang = match[1];
      if (!syntaxReady || !Highlighter) {
        return (
          <pre style={plainFenceStyle}>
            <code className={className} {...props}>
              {code}
            </code>
          </pre>
        );
      }
      return (
        <Highlighter
          code={code}
          language={lang}
          customStyle={highlighterCustom}
          preTagProps={{ [SYNTAX_BLOCK_ATTR]: true }}
        />
      );
    }
    return (
      <code className={className} {...props}>
        {children}
      </code>
    );
  };
}

/**
 * Schemes that are internal machinery, never destinations.
 *
 * `attachment:` is the one that bites. Inline cards are positioned in assistant
 * prose with an `<attachment:id/>` tag (see @pyxis-llm/core's `messageContentTags`),
 * and GFM treats `<scheme:rest>` as AUTOLINK syntax — so any surface that
 * renders that prose as markdown turns the tag into `<a href="attachment:id/">`.
 * Only a message-rendering surface that has already stripped/consumed the tags
 * should ever see clean prose; every other markdown surface (run transcript,
 * activity stream, report, thinking panel) renders the raw content and would
 * otherwise produce a clickable link to a scheme no browser can resolve.
 * Clicking it did nothing at best, and in an Electron shell a navigation to an
 * unknown scheme is not harmless.
 *
 * Rendering these inert (plain text, not an anchor) is the safety net.
 */
const INERT_LINK_SCHEMES = ['attachment:'];

function isInertHref(href: string | undefined): boolean {
  if (!href) return false;
  const normalized = href.trim().toLowerCase();
  return INERT_LINK_SCHEMES.some((scheme) => normalized.startsWith(scheme));
}

/** What a host's link renderer receives: the link as markdown produced it. */
export type MarkdownLinkRendererProps = React.ComponentPropsWithoutRef<'a'>;

/**
 * Lets a host render markdown links its own way (e.g. as chips with an icon
 * for the thing linked to) everywhere this package renders markdown - chat
 * messages, streaming text, transcripts - without threading a prop through
 * each of them. Unset (the default) renders a plain anchor. Inert schemes
 * (see below) never reach the renderer.
 */
export const MarkdownLinkRendererContext = React.createContext<
  React.ComponentType<MarkdownLinkRendererProps> | null
>(null);

function Anchor({ href, children, ...props }: React.ComponentPropsWithoutRef<'a'>) {
  const Renderer = React.useContext(MarkdownLinkRendererContext);
  if (isInertHref(href)) {
    return <span style={{ opacity: 0.6 }}>{children}</span>;
  }
  if (Renderer) {
    return (
      <Renderer href={href} {...props}>
        {children}
      </Renderer>
    );
  }
  return (
    <a href={href} {...props}>
      {children}
    </a>
  );
}

/**
 * Components to pass to ReactMarkdown so fenced code blocks (e.g. ```js, ```python)
 * are rendered with syntax highlighting. Inline code is left as plain <code>.
 */
export const markdownCodeComponents: Components = {
  pre: Pre,
  a: Anchor,
  code: createCodeComponent(PLAIN_FENCE_DEFAULT, { ...HIGHLIGHTER_CUSTOM_BASE }),
};

/**
 * Same as {@link markdownCodeComponents} but code fences wrap instead of scrolling horizontally,
 * for a narrow live-streaming markdown surface.
 */
export const markdownThinkingStreamCodeComponents: Components = {
  pre: Pre,
  a: Anchor,
  code: createCodeComponent(PLAIN_FENCE_THINKING_STREAM, HIGHLIGHTER_CUSTOM_THINKING_STREAM),
};

/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode, type Ref } from 'react';
import { ActionIcon, Box, Group, Loader, SegmentedControl, Text, Textarea, useComputedColorScheme } from '@mantine/core';
import { useDebouncedValue } from '@mantine/hooks';
import { renderVegaChart, resolveChartFont, type VegaDataLoader, type VegaMode } from './vegaRuntime.js';

/**
 * Fences are re-rendered on every streamed delta, and a half-written spec is
 * not valid JSON; wait for the text to settle before parsing and rendering.
 */
const VEGA_DEBOUNCE_MS = 320;

export type VegaChartProps = {
  /** The spec as JSON text (a fence body, or JSON.stringify of a card's spec). */
  source: string;
  mode?: VegaMode;
  /** Header label; defaults to the spec's own title, then "Chart". */
  title?: string;
  caption?: string;
  defaultView?: 'chart' | 'source';
  /** When set, the Source view is an editable textarea and this receives each edit. Omit for the read-only view. */
  onSourceChange?: (next: string) => void;
  /** Disables the editable Source textarea. Only meaningful with onSourceChange. */
  readOnly?: boolean;
  /** Called when the editable Source textarea gains focus. */
  onSourceFocus?: () => void;
  /** Ref to the editable Source textarea, for callers that manage focus. */
  sourceInputRef?: Ref<HTMLTextAreaElement>;
  /** Extra control rendered in the footer next to the Chart/Source toggle. */
  footerAction?: ReactNode;
  /** Host loader for data urls. Omitted: every load is refused (chat). Keep the reference stable. */
  loader?: VegaDataLoader;
};

type Parsed =
  | { kind: 'empty' }
  | { kind: 'invalid' }
  | { kind: 'ok'; spec: Record<string, unknown> };

function parseSpec(source: string): Parsed {
  const trimmed = source.trim();
  if (!trimmed) return { kind: 'empty' };
  try {
    const value = JSON.parse(trimmed) as unknown;
    if (value === null || typeof value !== 'object' || Array.isArray(value)) return { kind: 'invalid' };
    return { kind: 'ok', spec: value as Record<string, unknown> };
  } catch {
    return { kind: 'invalid' };
  }
}

function specTitle(spec: Record<string, unknown> | undefined): string | undefined {
  const t = spec?.title;
  if (typeof t === 'string' && t.trim()) return t.trim();
  if (t && typeof t === 'object' && typeof (t as { text?: unknown }).text === 'string') {
    return (t as { text: string }).text.trim() || undefined;
  }
  return undefined;
}

/* ---- Chart text size: one setting shared by every chart, remembered. ---- */

export const VEGA_FONT_SCALES = [0.8, 0.9, 1, 1.15, 1.3, 1.5, 1.75] as const;
const FONT_SCALE_KEY = 'llmd.vegaChart.fontScale';
const fontScaleListeners = new Set<() => void>();
let fontScale = readStoredFontScale();

function readStoredFontScale(): number {
  try {
    const v = Number(globalThis.localStorage?.getItem(FONT_SCALE_KEY));
    return (VEGA_FONT_SCALES as readonly number[]).includes(v) ? v : 1;
  } catch {
    return 1;
  }
}

function setFontScale(next: number): void {
  fontScale = next;
  try {
    globalThis.localStorage?.setItem(FONT_SCALE_KEY, String(next));
  } catch {
    // Storage unavailable (private mode, tests): the setting lasts the session.
  }
  for (const l of fontScaleListeners) l();
}

function useFontScale(): number {
  return useSyncExternalStore(
    (l) => {
      fontScaleListeners.add(l);
      return () => fontScaleListeners.delete(l);
    },
    () => fontScale,
    () => 1
  );
}

function stepFontScale(current: number, dir: 1 | -1): number {
  const scales = VEGA_FONT_SCALES as readonly number[];
  const i = scales.indexOf(current);
  const at = i < 0 ? scales.indexOf(1) : i;
  return scales[Math.min(scales.length - 1, Math.max(0, at + dir))];
}

/* ---- Layout CSS, injected once. vega-embed puts its \`vega-embed\` class on
   the HOST element itself (the div we hand it), so its rules are matched as
   \`.vega-chart-embed__host.vega-embed\` (compound), never as a descendant.
   Selectors carry extra classes so they beat vega-embed's own stylesheet,
   which it injects later. The host is made position: static so the absolutely
   positioned … button resolves against the card (.vega-chart-embed) and can
   sit in the footer row at the card's bottom-right. ---- */

const STYLE_ID = 'llmd-vega-chart-styles';
const H = '.vega-chart-embed .vega-chart-embed__host.vega-embed';
const CHART_CSS = `
.vega-chart-embed { position: relative; }
.vega-chart-embed .vega-chart-embed__host { display: flex; justify-content: center; }
${H} { position: static; display: flex; justify-content: center; max-width: 100%; }
${H}.has-actions { padding-right: 0; }
${H} summary { top: auto; bottom: 0; right: 0; opacity: 0.6; }
.vega-chart-embed:hover ${H.slice('.vega-chart-embed '.length)} summary { opacity: 1 !important; }
${H} .vega-actions { top: auto; bottom: 36px; right: -9px; }
${H} .vega-actions a { font-family: inherit; }
${H} .vega-actions::before {
  top: auto; bottom: -16px; border-bottom-color: transparent; border-top-color: #d9d9d9;
}
${H} .vega-actions::after {
  top: auto; bottom: -14px; border-bottom-color: transparent; border-top-color: #fff;
}
.vega-chart-embed .vega-chart-embed__footer { min-height: 28px; padding-right: 36px; }
`;

function ensureChartStyles(): void {
  if (typeof document === 'undefined' || document.getElementById(STYLE_ID)) return;
  const style = document.createElement('style');
  style.id = STYLE_ID;
  style.textContent = CHART_CSS;
  document.head.appendChild(style);
}

export function VegaChart({
  source,
  mode = 'vega-lite',
  title,
  caption,
  defaultView = 'chart',
  onSourceChange,
  readOnly,
  onSourceFocus,
  sourceInputRef,
  footerAction,
  loader,
}: VegaChartProps) {
  ensureChartStyles();
  const isDark = useComputedColorScheme('light') === 'dark';
  const scale = useFontScale();
  const hostRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<'chart' | 'source'>(defaultView);
  const [debounced] = useDebouncedValue(source, VEGA_DEBOUNCE_MS);
  const parsed = useMemo(() => parseSpec(debounced), [debounced]);
  const [rendering, setRendering] = useState(false);
  const [renderError, setRenderError] = useState<string | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (view !== 'chart' || !host || parsed.kind !== 'ok') {
      setRendering(false);
      setRenderError(null);
      return;
    }
    let cancelled = false;
    let finalize: (() => void) | undefined;
    setRendering(true);
    setRenderError(null);
    void renderVegaChart(host, parsed.spec, {
      mode,
      isDark,
      downloadFileName: specTitle(parsed.spec) ?? title,
      fontScale: scale,
      loader,
    }).then((result) => {
      if (cancelled) {
        if ('finalize' in result) result.finalize();
        return;
      }
      if ('finalize' in result) {
        finalize = result.finalize;
        if (result.problems.length > 0) setRenderError(result.problems.join('\n'));
      } else {
        setRenderError(result.error);
      }
      setRendering(false);
    });
    return () => {
      cancelled = true;
      finalize?.();
      host.innerHTML = '';
    };
  }, [parsed, mode, isDark, view, title, scale, loader]);

  const ownTitle = specTitle(parsed.kind === 'ok' ? parsed.spec : undefined);
  const label = title?.trim() || ownTitle || 'Chart';
  // The chart draws its own title; repeating it in the header showed it twice.
  const explicit = title?.trim();
  const headerLabel =
    explicit && explicit !== ownTitle ? explicit : view === 'chart' && ownTitle ? undefined : explicit || ownTitle;
  // A fence's chart replaces a code block, whose monospace font it would
  // otherwise inherit. Same font as the chart's own labels.
  const font = useMemo(() => resolveChartFont(), []);

  return (
    <Box
      className="vega-chart-embed"
      style={{ display: 'flex', flexDirection: 'column', gap: 8, minWidth: 0, fontFamily: font }}
    >
      {headerLabel ? (
        <Text size="sm" fw={600} className="vega-chart-embed__label" style={{ fontFamily: font }}>
          {headerLabel}
        </Text>
      ) : null}

      {view === 'chart' ? (
        <div className="vega-chart-embed__chart" style={{ minWidth: 0 }}>
          {rendering ? (
            <Group gap="xs" className="vega-chart-embed__loading">
              <Loader size="xs" />
              <Text size="xs" c="dimmed">
                Rendering…
              </Text>
            </Group>
          ) : null}
          {/* vega-embed owns this node's children: React never renders into it. */}
          <div
            ref={hostRef}
            className="vega-chart-embed__host"
            role="img"
            aria-label={label}
            style={{ width: '100%', minWidth: 0 }}
          />
          {parsed.kind === 'empty' ? (
            <Text size="sm" c="dimmed" className="vega-chart-embed__empty">
              Chart source is empty.
            </Text>
          ) : null}
          {parsed.kind === 'invalid' ? (
            // Quiet on purpose: while a fence streams in, its JSON is incomplete.
            <Text size="sm" c="dimmed" className="vega-chart-embed__invalid">
              Chart spec is not a complete JSON object.
            </Text>
          ) : null}
          {renderError ? (
            <Text size="xs" c="red" className="vega-chart-embed__error" style={{ whiteSpace: 'pre-wrap' }}>
              {renderError}
            </Text>
          ) : null}
        </div>
      ) : onSourceChange ? (
        <Textarea
          ref={sourceInputRef}
          className="vega-chart-embed__source"
          value={source}
          autosize
          minRows={3}
          spellCheck={false}
          disabled={readOnly}
          onChange={(e) => onSourceChange(e.currentTarget.value)}
          onFocus={() => onSourceFocus?.()}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace, monospace)' } }}
        />
      ) : (
        <pre
          className="vega-chart-embed__source"
          style={{ margin: 0, overflowX: 'auto', fontFamily: 'var(--mantine-font-family-monospace, monospace)' }}
        >
          <code>{source}</code>
        </pre>
      )}

      {caption?.trim() ? (
        <Text size="xs" c="dimmed" className="vega-chart-embed__caption">
          {caption}
        </Text>
      ) : null}

      {/* Controls sit at the bottom; vega-embed's … menu is positioned (CSS
          above) at this row's right end, in the padding reserved for it. */}
      <Group justify="space-between" gap="xs" wrap="nowrap" className="vega-chart-embed__footer">
        <Group gap={4} wrap="nowrap" className="vega-chart-embed__font">
          <ActionIcon
            size="sm"
            variant="subtle"
            color="gray"
            aria-label="Smaller chart text"
            disabled={view !== 'chart' || scale <= VEGA_FONT_SCALES[0]}
            onClick={() => setFontScale(stepFontScale(scale, -1))}
          >
            <Text size="xs" fw={600} style={{ fontFamily: font }}>A−</Text>
          </ActionIcon>
          <ActionIcon
            size="sm"
            variant="subtle"
            color="gray"
            aria-label="Larger chart text"
            disabled={view !== 'chart' || scale >= VEGA_FONT_SCALES[VEGA_FONT_SCALES.length - 1]}
            onClick={() => setFontScale(stepFontScale(scale, 1))}
          >
            <Text size="sm" fw={600} style={{ fontFamily: font }}>A+</Text>
          </ActionIcon>
        </Group>
        <Group gap="xs" wrap="nowrap" className="vega-chart-embed__actions">
          {footerAction}
          <SegmentedControl
            size="xs"
            value={view}
            onChange={(v) => setView(v as 'chart' | 'source')}
            data={[
              { label: 'Chart', value: 'chart' },
              { label: 'Source', value: 'source' },
            ]}
            classNames={{ root: 'vega-chart-embed__toggle' }}
          />
        </Group>
      </Group>
    </Box>
  );
}

type BlockProps = { code: string; title?: string; caption?: string };

/** Renders ```vega-lite fences, and llmd chart cards in Vega-Lite format. */
export function VegaLiteMarkdownCodeBlock({ code, title, caption }: BlockProps) {
  return <VegaChart source={code} mode="vega-lite" title={title} caption={caption} />;
}

/** Renders ```vega fences (full Vega specs), and llmd chart cards in Vega format. */
export function VegaMarkdownCodeBlock({ code, title, caption }: BlockProps) {
  return <VegaChart source={code} mode="vega" title={title} caption={caption} />;
}

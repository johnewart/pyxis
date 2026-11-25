/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Vega / Vega-Lite rendering for chat: model-authored specs (```vega-lite
 * fences) and host-authored chart cards. A spec is UNTRUSTED input — it was
 * written by a model, or built from data a model influenced — so every path
 * by which a spec could reach outside the page is closed here, not left to
 * vega-embed's defaults:
 *
 *  - Network: the loader refuses every load, sanitize, http and file call.
 *    Vega routes data URLs, image-mark URLs AND link hrefs through
 *    `loader.sanitize`, so this one choke point stops fetches, beacons and
 *    navigation alike. Data must be inline (`data.values`).
 *    A host (a document editor, not chat) may pass `options.loader`. It is
 *    called only for data loads (`data.url`, context 'dataflow'); image-mark
 *    URLs and link hrefs go through sanitize and stay refused, because an
 *    image or link URL can leak or navigate, and data is all a host needs.
 *    The loader is a renderVegaChart option, so a spec cannot supply one:
 *    prepareVegaSpec strips `usermeta.embedOptions`.
 *  - Spec-controlled embed options: vega-embed merges
 *    `spec.usermeta.embedOptions` OVER the caller's options (loader, actions,
 *    editorUrl, ast, ...). prepareVegaSpec strips it.
 *  - Expressions: `ast: true` evaluates Vega expressions with the
 *    interpreter instead of compiling them with `new Function`, so no
 *    model-written code is ever compiled, and no `unsafe-eval` is needed.
 *  - Tooltips: vega-tooltip renders a datum's `image` field as
 *    `<img src=...>` (an arbitrary fetch on hover). formatTooltip below shows
 *    every field, `image` included, as escaped text.
 *  - Actions: only export (SVG/PNG, rendered locally). "Open in Vega Editor"
 *    would post the spec — data included — to an external site.
 */

import type { Loader, LoggerInterface } from 'vega';

type EmbedFn = typeof import('vega-embed').default;

export type VegaMode = 'vega-lite' | 'vega';

export type RenderVegaOptions = {
  mode: VegaMode;
  isDark: boolean;
  /** Base name for exported SVG/PNG files. */
  downloadFileName?: string;
  /**
   * CSS font-family for every chart label. Default: the page body's computed
   * font (the host app's UI font, including a user-chosen one).
   */
  fontFamily?: string;
  /** Multiplier for every text size in the chart (the card's A−/A+). Default 1. */
  fontScale?: number;
  /**
   * Host-supplied loader for DATA loads (`data.url`) only. Omitted: every
   * load is refused. Image marks and link hrefs stay refused either way.
   */
  loader?: VegaDataLoader;
};

/** Returns the text of a data url, or throws to refuse it. */
export type VegaDataLoader = (url: string) => Promise<string>;

/** Vega-Lite's default text sizes, so a font scale also moves unstyled text. */
const DEFAULT_FONT_SIZES = {
  title: { fontSize: 13, subtitleFontSize: 10 },
  axis: { labelFontSize: 10, titleFontSize: 11 },
  legend: { labelFontSize: 10, titleFontSize: 11 },
  header: { labelFontSize: 10, titleFontSize: 11 },
  text: { fontSize: 11 },
};

const FONT_SIZE_KEY = /^(fontSize|[a-z]+FontSize)$/;

/** Multiply every numeric *fontSize anywhere in `node` by `scale`, in place. */
function scaleFontSizes(node: unknown, scale: number): void {
  if (Array.isArray(node)) {
    for (const v of node) scaleFontSizes(v, scale);
    return;
  }
  if (!isPlainObject(node)) return;
  for (const key of Object.keys(node)) {
    const v = node[key];
    if (FONT_SIZE_KEY.test(key) && typeof v === 'number') node[key] = Math.round(v * scale * 10) / 10;
    else if (key !== 'data' && key !== 'datasets') scaleFontSizes(v, scale);
  }
}

const FALLBACK_CHART_FONT = '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';

/**
 * The font charts use: `explicit`, else the page body's computed font. Vega
 * draws labels as SVG text with their own font attribute, so the CSS font of
 * the container (which may be a monospace code-block style) never applies;
 * the body is the app's UI font.
 */
export function resolveChartFont(explicit?: string): string {
  const given = explicit?.trim();
  if (given) return given;
  if (typeof document !== 'undefined' && document.body && typeof getComputedStyle === 'function') {
    const body = getComputedStyle(document.body).fontFamily?.trim();
    if (body) return body;
  }
  return FALLBACK_CHART_FONT;
}

/**
 * Every config slot Vega / Vega-Lite reads a font from, set to `font`, with
 * the default text sizes multiplied by `scale`.
 */
export function chartFontConfig(font: string, scale = 1): Record<string, unknown> {
  const s = (n: number) => Math.round(n * scale * 10) / 10;
  const d = DEFAULT_FONT_SIZES;
  return {
    font,
    title: { font, subtitleFont: font, fontSize: s(d.title.fontSize), subtitleFontSize: s(d.title.subtitleFontSize) },
    axis: { labelFont: font, titleFont: font, labelFontSize: s(d.axis.labelFontSize), titleFontSize: s(d.axis.titleFontSize) },
    legend: { labelFont: font, titleFont: font, labelFontSize: s(d.legend.labelFontSize), titleFontSize: s(d.legend.titleFontSize) },
    header: { labelFont: font, titleFont: font, labelFontSize: s(d.header.labelFontSize), titleFontSize: s(d.header.titleFontSize) },
    mark: { font },
    text: { font, fontSize: s(d.text.fontSize) },
  };
}

const FONT_KEY = /^(font|labelFont|titleFont|subtitleFont)$/;

/** Drop font choices from a spec's config (recursively) and its mark, so the app font wins. */
function stripFonts(node: unknown): void {
  if (!isPlainObject(node)) return;
  for (const key of Object.keys(node)) {
    if (FONT_KEY.test(key)) delete node[key];
    else stripFonts(node[key]);
  }
}

/**
 * `problems`: things the user should know about a chart that DID render —
 * chiefly a blocked external load, which Vega only logs (as a warning) and
 * then draws the chart with no data.
 */
export type RenderVegaResult = { finalize: () => void; problems: string[] } | { error: string };

export const VEGA_EXTERNAL_RESOURCE_ERROR =
  'Charts in chat cannot load external resources. Put the data inline under "data": {"values": [...]}.';

const refuse = async (): Promise<never> => {
  throw new Error(VEGA_EXTERNAL_RESOURCE_ERROR);
};

const BLOCKED_LOADER: Loader = {
  load: refuse,
  sanitize: refuse,
  http: refuse,
  file: refuse,
};

/** A Vega loader that sends data loads to `dataLoader`; everything else is refused. */
function loaderFor(dataLoader: VegaDataLoader | undefined, problems: string[]): Loader {
  // Vega swallows a refused href (it only fires on click), so sanitize also
  // reports the refusal to the caller.
  const refuseAndReport = async (): Promise<never> => {
    if (!problems.includes(VEGA_EXTERNAL_RESOURCE_ERROR)) problems.push(VEGA_EXTERNAL_RESOURCE_ERROR);
    return refuse();
  };
  if (!dataLoader) return { ...BLOCKED_LOADER, sanitize: refuseAndReport };
  return {
    // Vega calls load() directly only for dataflow requests; images and
    // hrefs go through sanitize() first, which stays refused.
    load: async (uri: string, options?: { context?: string }) => {
      if (options?.context !== 'dataflow') return refuse();
      return dataLoader(uri);
    },
    sanitize: refuseAndReport,
    http: refuse,
    file: refuse,
  } as unknown as Loader;
}

let embedPromise: Promise<EmbedFn> | null = null;

function loadVegaEmbed(): Promise<EmbedFn> {
  if (!embedPromise) {
    embedPromise = import('vega-embed').then(
      (mod) => mod.default,
      (err: unknown) => {
        // Don't cache a failed import (e.g. the optional peer isn't installed
        // yet during dev); the next chart retries.
        embedPromise = null;
        throw err;
      }
    );
  }
  return embedPromise;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

const FACET_CHANNELS = ['row', 'column', 'facet'];

/**
 * A deep copy of `spec` that is safe to hand to vega-embed: spec-supplied
 * embed options removed, and a single-view Vega-Lite chart with no explicit
 * width sized to its container (chat bubbles vary in width). Never mutates
 * the input.
 */
export function prepareVegaSpec(
  spec: Record<string, unknown>,
  mode: VegaMode,
  fontScale = 1
): Record<string, unknown> {
  const out = JSON.parse(JSON.stringify(spec)) as Record<string, unknown>;
  if (isPlainObject(out.usermeta)) {
    delete out.usermeta.embedOptions;
  }
  // Charts use the app's font, whatever the spec asks for (models like
  // "monospace"). Config-level and mark-level choices are dropped; the font
  // comes from renderVegaChart's embed config.
  stripFonts(out.config);
  if (isPlainObject(out.mark)) {
    delete out.mark.font;
  }
  if (isPlainObject(out.title)) {
    delete out.title.font;
    delete out.title.subtitleFont;
  }
  // Sizes the spec sets itself scale too, or A−/A+ would skip them.
  if (fontScale !== 1) scaleFontSizes(out, fontScale);
  if (mode === 'vega-lite' && out.width === undefined && ('mark' in out || 'layer' in out)) {
    const encoding = isPlainObject(out.encoding) ? out.encoding : {};
    const faceted = FACET_CHANNELS.some((ch) => ch in encoding);
    if (!faceted) out.width = 'container';
  }
  return out;
}

function stringifyForTooltip(value: unknown): string {
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
}

/**
 * vega-tooltip's default formatter minus the `image` special case: every
 * field renders as escaped text through `valueToHtml` (vega-tooltip's HTML
 * escaper), so a tooltip never fetches anything.
 */
export function formatTooltip(
  value: unknown,
  valueToHtml: (value: unknown) => string,
  _maxDepth?: number,
  _baseURL?: string
): string {
  if (Array.isArray(value)) {
    return `[${value
      .map((v) => valueToHtml(typeof v === 'string' ? v : stringifyForTooltip(v)))
      .join(', ')}]`;
  }
  if (isPlainObject(value)) {
    const { title, ...rest } = value;
    let html = title !== undefined && title !== null && title !== '' ? `<h2>${valueToHtml(title)}</h2>` : '';
    const keys = Object.keys(rest).filter((k) => rest[k] !== undefined);
    if (keys.length > 0) {
      html += '<table>';
      for (const key of keys) {
        const raw = rest[key];
        const cell = raw !== null && typeof raw === 'object' ? stringifyForTooltip(raw) : raw;
        html += `<tr><td class="key">${valueToHtml(key)}</td><td class="value">${valueToHtml(cell)}</td></tr>`;
      }
      html += '</table>';
    }
    return html || '{}';
  }
  return valueToHtml(value);
}

function logArgText(arg: unknown): string {
  if (arg instanceof Error) return arg.message;
  return typeof arg === 'string' ? arg : stringifyForTooltip(arg);
}

/**
 * A Vega logger that records errors, and warnings that carry an Error (failed
 * loads / ingestion), instead of printing them. Vega's dataflow calls these
 * with `this` bound to itself, so nothing here may rely on `this`.
 */
function collectingLogger(problems: string[]): LoggerInterface {
  let currentLevel = 1;
  const record = (args: unknown[]) => {
    const blocked = args.some((a) => a instanceof Error && a.message === VEGA_EXTERNAL_RESOURCE_ERROR);
    const text = blocked ? VEGA_EXTERNAL_RESOURCE_ERROR : args.map(logArgText).join(' ');
    if (!problems.includes(text)) problems.push(text);
  };
  const logger = {
    level(level?: number) {
      if (level === undefined) return currentLevel;
      currentLevel = level;
      return logger;
    },
    error(...args: unknown[]) {
      record(args);
      return logger;
    },
    warn(...args: unknown[]) {
      if (args.some((a) => a instanceof Error)) record(args);
      return logger;
    },
    info() {
      return logger;
    },
    debug() {
      return logger;
    },
  };
  return logger as unknown as LoggerInterface;
}

/** The locked-down embed options shared by on-screen and headless rendering. */
function lockedEmbedOptions(
  options: { mode: VegaMode; fontFamily?: string; loader?: VegaDataLoader },
  scale: number,
  problems: string[]
) {
  return {
    mode: options.mode,
    renderer: 'svg' as const,
    ast: true, // expression interpreter, never `new Function`
    loader: loaderFor(options.loader, problems),
    config: { background: 'transparent', ...chartFontConfig(resolveChartFont(options.fontFamily), scale) },
    // Problems surface to the caller, not the console.
    logger: collectingLogger(problems),
  };
}

export type RenderVegaSvgOptions = {
  mode: VegaMode;
  /** Fixed chart width replacing "container". Default 640. */
  width?: number;
  fontFamily?: string;
};

export type RenderVegaSvgResult = { svg: string; problems: string[] } | { error: string };

/**
 * Render `spec` to a static SVG string (light theme) under the same lockdown
 * as renderVegaChart. Never throws.
 */
export async function renderVegaSvg(
  spec: Record<string, unknown>,
  options: RenderVegaSvgOptions
): Promise<RenderVegaSvgResult> {
  const problems: string[] = [];
  let finalize: (() => void) | undefined;
  try {
    const embed = await loadVegaEmbed();
    const prepared = prepareVegaSpec(spec, options.mode);
    if (prepared.width === 'container') {
      prepared.width = options.width && options.width > 0 ? options.width : 640;
    }
    const host = document.createElement('div');
    const result = await embed(host, prepared as Parameters<EmbedFn>[1], {
      ...lockedEmbedOptions(options, 1, problems),
      actions: false,
      tooltip: false,
    });
    finalize = result.finalize;
    const svg = await result.view.toSVG();
    return { svg, problems };
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  } finally {
    try {
      finalize?.();
    } catch (e) {
      problems.push(`finalize failed: ${e instanceof Error ? e.message : String(e)}`);
    }
  }
}

/**
 * Render `spec` into `el` (whose children vega-embed owns from here on).
 * Never throws: failures come back as `{ error }` with `el` cleared.
 */
export async function renderVegaChart(
  el: HTMLElement,
  spec: Record<string, unknown>,
  options: RenderVegaOptions
): Promise<RenderVegaResult> {
  const problems: string[] = [];
  try {
    const embed = await loadVegaEmbed();
    const scale = options.fontScale && options.fontScale > 0 ? options.fontScale : 1;
    const prepared = prepareVegaSpec(spec, options.mode, scale);
    const result = await embed(el, prepared as Parameters<EmbedFn>[1], {
      ...lockedEmbedOptions(options, scale, problems),
      actions: { export: { svg: true, png: true }, source: false, compiled: false, editor: false },
      ...(options.isDark ? { theme: 'dark' as const } : {}),
      tooltip: { formatTooltip, theme: options.isDark ? 'dark' : 'light' },
      downloadFileName: options.downloadFileName || 'chart',
    });
    return { finalize: result.finalize, problems };
  } catch (e) {
    el.innerHTML = '';
    return { error: e instanceof Error ? e.message : String(e) };
  }
}

/** Drop the cached vega-embed import (tests / hot reload). */
export function resetVegaRuntimeForTests(): void {
  embedPromise = null;
}

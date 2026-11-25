/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * The `chart` display card type (see displayCard.ts): an interactive
 * Vega-Lite (or full Vega) visualization. The spec — and, more importantly,
 * its data — is display-only, so a host tool can chart thousands of rows
 * without the model ever seeing them.
 *
 * Data must be INLINE (`data.values`). Renderers refuse to fetch anything a
 * spec points at (data URLs, image marks, links), so a chart can never
 * become a beacon. The spec is capped (CHART_CARD_MAX_SPEC_BYTES) because it
 * is stored in the session DB and pushed over the live attachment stream.
 *
 * Rendering needs the charts plugin (`@pyxis-llm/react/charts`). Small,
 * model-authored charts do not need a card at all: a ```vega-lite fence in
 * assistant prose renders through the same plugin. The trade-off is that
 * fence data is part of the message, so the model re-reads it every turn.
 */

import { type DisplayCard, utf8Length } from './displayCard.js';

export const CHART_CARD_TYPE = 'chart';

export const CHART_CARD_FORMATS = ['vega-lite', 'vega'] as const;

export type ChartCardFormat = (typeof CHART_CARD_FORMATS)[number];

/** Largest serialized spec accepted (1 MiB of JSON, data included). */
export const CHART_CARD_MAX_SPEC_BYTES = 1024 * 1024;

export interface ChartBody {
  /** Default 'vega-lite'. */
  format: ChartCardFormat;
  /** The Vega-Lite / Vega spec, as a JSON object. */
  spec: Record<string, unknown>;
}

export type ChartCard = DisplayCard<ChartBody>;

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function isChartCardFormat(value: unknown): value is ChartCardFormat {
  return typeof value === 'string' && (CHART_CARD_FORMATS as readonly string[]).includes(value);
}

/**
 * Why `spec` is not an acceptable chart spec, or undefined when it is.
 * Producers can call this before persisting to fail with a clear message.
 */
export function chartCardSpecProblem(spec: unknown): string | undefined {
  if (!isPlainObject(spec)) return 'chart spec must be a JSON object';
  if (Object.keys(spec).length === 0) return 'chart spec is empty';
  const bytes = utf8Length(JSON.stringify(spec));
  if (bytes > CHART_CARD_MAX_SPEC_BYTES) {
    return `chart spec is ${bytes} bytes; the limit is ${CHART_CARD_MAX_SPEC_BYTES}`;
  }
  return undefined;
}

/**
 * A display card body as a chart, or undefined when it is not a valid,
 * in-limits one. A missing `format` means Vega-Lite; an unknown one is rejected.
 */
export function decodeChartBody(body: Record<string, unknown>): ChartBody | undefined {
  const format = body.format === undefined ? 'vega-lite' : body.format;
  if (!isChartCardFormat(format)) return undefined;
  if (chartCardSpecProblem(body.spec) !== undefined) return undefined;
  return { format, spec: body.spec as Record<string, unknown> };
}

/** The chart's own title (spec.title as a string or {text}), if it has one. */
export function chartSpecTitle(spec: Record<string, unknown> | undefined): string | undefined {
  const t = spec?.title;
  if (typeof t === 'string' && t.trim()) return t.trim();
  if (isPlainObject(t) && typeof t.text === 'string') return t.text.trim() || undefined;
  return undefined;
}

/** A chart display card (type CHART_CARD_TYPE). */
export function chartDisplayCard(
  id: string,
  body: ChartBody,
  meta: { title?: string; caption?: string } = {}
): ChartCard {
  return { id, type: CHART_CARD_TYPE, body, ...meta };
}

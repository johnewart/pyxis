/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit-speed (no real llmd binary). @pyxis-llm/core's display card envelope and
 * its two built-in body types (image, chart): what is accepted, what is
 * refused, and that the provider-facing summary never carries the body.
 */

import { describe, expect, it } from 'vitest';
import {
  CHART_CARD_MAX_SPEC_BYTES,
  DISPLAY_CARD_KIND,
  DISPLAY_CARD_MAX_PAYLOAD_BYTES,
  IMAGE_CARD_MAX_BYTES,
  base64DecodedLength,
  buildDisplayCardAttachment,
  chartCardSpecProblem,
  chartDisplayCard,
  chartSpecTitle,
  decodeChartBody,
  decodeDisplayCard,
  decodeImageBody,
  encodeDisplayCard,
  imageCardDataUrl,
  imageCardSrcProblem,
  imageDisplayCard,
  synthesizeDisplayCardContent,
} from '@pyxis-llm/core';

// 1x1 transparent PNG.
const PNG_B64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=';

describe('display card envelope', () => {
  it('round-trips any type, leaving the body to the type', () => {
    const card = { id: 'b1', type: 'budget-table', title: 'October', body: { rows: [1, 2] } };
    expect(decodeDisplayCard(encodeDisplayCard(card))).toEqual(card);
  });

  it('rejects payloads without an id, a type or an object body, and oversized ones', () => {
    expect(decodeDisplayCard('not json')).toBeUndefined();
    expect(decodeDisplayCard(JSON.stringify({ type: 'x', body: {} }))).toBeUndefined();
    expect(decodeDisplayCard(JSON.stringify({ id: 'a', type: ' ', body: {} }))).toBeUndefined();
    expect(decodeDisplayCard(JSON.stringify({ id: 'a', type: 'x', body: [1] }))).toBeUndefined();
    const huge = JSON.stringify({ id: 'a', type: 'x', body: { s: 'x'.repeat(DISPLAY_CARD_MAX_PAYLOAD_BYTES) } });
    expect(decodeDisplayCard(huge)).toBeUndefined();
  });

  it('builds an attachment whose provider content never includes the body', () => {
    const card = { id: 'b1', type: 'budget-table', title: 'October', caption: 'Rent is up', body: { secret: 42 } };
    const content = synthesizeDisplayCardContent(card);
    const att = buildDisplayCardAttachment(card, { afterToolCallId: 'call_1', content });
    expect(att.kind).toBe(DISPLAY_CARD_KIND);
    expect(att.afterToolCallId).toBe('call_1');
    expect(content).toBe('[Shown to the user: budget-table "October" — Rent is up]');
    expect(content).not.toContain('secret');
    expect(buildDisplayCardAttachment(card).content).toBeUndefined();
  });
});

describe('image body', () => {
  const body = { src: imageCardDataUrl('image/png', PNG_B64), mimeType: 'image/png' as const, fileName: 'spend.png', width: 1, height: 1 };

  it('round-trips through the envelope', () => {
    const card = imageDisplayCard('img-1', body, { title: 'Monthly spend' });
    const env = decodeDisplayCard(encodeDisplayCard(card))!;
    expect(env.type).toBe('image');
    expect(decodeImageBody(env.body)).toEqual(body);
  });

  it('derives mimeType from src, ignoring a stored mismatch', () => {
    expect(decodeImageBody({ ...body, mimeType: 'image/gif' })?.mimeType).toBe('image/png');
  });

  it('refuses anything that is not a base64 data: URL of an allowed type', () => {
    expect(imageCardSrcProblem('https://example.test/p.png')).toMatch(/data: URL/);
    expect(imageCardSrcProblem('data:text/html;base64,PGgxPmhpPC9oMT4=')).toMatch(/unsupported image type/);
    expect(imageCardSrcProblem('data:image/png;base64,not base64!')).toMatch(/not valid base64/);
    expect(imageCardSrcProblem('data:image/png;base64,')).toMatch(/empty/);
    expect(imageCardSrcProblem(imageCardDataUrl('image/svg+xml', 'PHN2Zy8+'))).toBeUndefined();
    expect(decodeImageBody({ src: 'https://example.test/p.png' })).toBeUndefined();
  });

  it('enforces the size cap on decoded bytes', () => {
    const chunks = Math.ceil((IMAGE_CARD_MAX_BYTES + 3) / 3);
    const tooBig = 'AAAA'.repeat(chunks);
    expect(base64DecodedLength(tooBig)).toBeGreaterThan(IMAGE_CARD_MAX_BYTES);
    expect(imageCardSrcProblem(imageCardDataUrl('image/png', tooBig))).toMatch(/limit/);
  });
});

describe('chart body', () => {
  const spec = {
    title: 'Spend by category',
    data: { values: [{ c: 'food', v: 3 }, { c: 'rent', v: 9 }] },
    mark: 'bar',
  };

  it('round-trips through the envelope', () => {
    const card = chartDisplayCard('chart-1', { format: 'vega-lite', spec }, { caption: 'Q3' });
    const env = decodeDisplayCard(encodeDisplayCard(card))!;
    expect(env.type).toBe('chart');
    expect(decodeChartBody(env.body)).toEqual({ format: 'vega-lite', spec });
  });

  it('defaults a missing format to vega-lite and rejects unknown formats', () => {
    expect(decodeChartBody({ spec })?.format).toBe('vega-lite');
    expect(decodeChartBody({ spec, format: 'plotly' })).toBeUndefined();
  });

  it('refuses non-object, empty and oversized specs', () => {
    expect(chartCardSpecProblem([1, 2])).toMatch(/JSON object/);
    expect(chartCardSpecProblem({})).toMatch(/empty/);
    const big = { data: { values: [{ s: 'x'.repeat(CHART_CARD_MAX_SPEC_BYTES) }] }, mark: 'bar' };
    expect(chartCardSpecProblem(big)).toMatch(/limit/);
    expect(decodeChartBody({ spec: big })).toBeUndefined();
  });

  it('reads the chart title from a string or {text}', () => {
    expect(chartSpecTitle(spec)).toBe('Spend by category');
    expect(chartSpecTitle({ title: { text: 'T' } })).toBe('T');
    expect(chartSpecTitle({})).toBeUndefined();
  });
});
